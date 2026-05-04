// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package managedseedset

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	controllermanagerconfigv1alpha1 "github.com/gardener/gardener/pkg/apis/config/controllermanager/v1alpha1"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
	gardenerutils "github.com/gardener/gardener/pkg/utils/gardener"
)

// Actuator acts upon ManagedSeedSet resources.
type Actuator interface {
	// Reconcile reconciles ManagedSeedSet creation, update, or deletion.
	Reconcile(context.Context, logr.Logger, *seedmanagementv1alpha1.ManagedSeedSet) (*seedmanagementv1alpha1.ManagedSeedSetStatus, bool, error)
}

// actuator is a concrete implementation of Actuator.
type actuator struct {
	gardenClient   client.Client
	replicaGetter  ReplicaGetter
	replicaFactory ReplicaFactory
	cfg            *controllermanagerconfigv1alpha1.ManagedSeedSetControllerConfiguration
	recorder       events.EventRecorder
}

// NewActuator creates and returns a new Actuator with the given parameters.
func NewActuator(
	gardenClient client.Client,
	replicaGetter ReplicaGetter,
	replicaFactory ReplicaFactory,
	cfg *controllermanagerconfigv1alpha1.ManagedSeedSetControllerConfiguration,
	recorder events.EventRecorder,
) Actuator {
	return &actuator{
		gardenClient:   gardenClient,
		replicaFactory: replicaFactory,
		replicaGetter:  replicaGetter,
		cfg:            cfg,
		recorder:       recorder,
	}
}

// Now returns the current local time. Exposed for testing.
var Now = metav1.Now

// Reconcile reconciles ManagedSeedSet creation or update.
func (a *actuator) Reconcile(ctx context.Context, log logr.Logger, managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet) (status *seedmanagementv1alpha1.ManagedSeedSetStatus, removeFinalizer bool, err error) {
	// Initialize status
	status = managedSeedSet.Status.DeepCopy()
	status.ObservedGeneration = managedSeedSet.Generation

	defer func() {
		if err != nil {
			a.errorEventf(managedSeedSet, gardencorev1beta1.EventReconcileError, gardencorev1beta1.EventActionReconcile, err.Error())
		}
	}()

	// Manage ControllerRevisions:
	// 1. Calculate spec template hash (template + shootTemplate).
	// 2. Detect name collisions and adjust CollisionCount.
	// 3. Update CurrentRevision and UpdateRevision in status.
	// 4. Create a new ControllerRevision when the spec changes (triggers rolling update).
	// 5. Adopt / bump an existing ControllerRevision when its data matches but it is stale
	//    (rollback case: makes that revision the update target again).
	if err := getMSSRevisions(ctx, a.gardenClient, managedSeedSet, status); err != nil {
		return status, false, fmt.Errorf("failed to manage ControllerRevisions: %w", err)
	}

	// Extract the hash portions from the current and update revision names.
	updateRevisionHash := revisionHashFromName(status.UpdateRevision, managedSeedSet.Name)
	currentRevisionHash := revisionHashFromName(status.CurrentRevision, managedSeedSet.Name)

	// Get replicas
	replicas, err := a.replicaGetter.GetReplicas(ctx, managedSeedSet)
	if err != nil {
		return status, false, err
	}

	// Sort replicas by ascending ordinal
	sort.Sort(ascendingOrdinal(replicas))

	// Get the pending replica, if any
	pendingReplica := getPendingReplica(replicas, status)

	// Determine ready, postponed, and deletable replicas
	var readyReplicas, postponedReplicas, deletableReplicas []Replica
	for _, r := range replicas {
		if replicaIsReady(r) {
			readyReplicas = append(readyReplicas, r)
		} else if r != pendingReplica {
			postponedReplicas = append(postponedReplicas, r)
		}
		if r.IsDeletable() {
			deletableReplicas = append(deletableReplicas, r)
		}
		debugReplica(r, log.V(1))
	}
	log.V(1).Info("Current replicas of ManagedSeedSet", "readyReplicas", readyReplicas, "postponedReplicas", postponedReplicas, "deletableReplicas", deletableReplicas)

	// Update replicas, readyReplicas, currentReplicas, and updatedReplicas counts in status.
	status.Replicas = int32(len(replicas))           // #nosec G115 -- `ra.replicaGetter.GetReplicas(ctx, managedSeedSet)` returns a line for every ManagedSeeds in the system. This number cannot exceed max int32.
	status.ReadyReplicas = int32(len(readyReplicas)) // #nosec G115 -- `ra.replicaGetter.GetReplicas(ctx, managedSeedSet)` returns a line for every ManagedSeeds in the system. This number cannot exceed max int32.
	var currentReplicas, updatedReplicas int32
	for _, r := range replicas {
		switch r.GetRevisionHash() {
		case updateRevisionHash:
			updatedReplicas++
		case currentRevisionHash:
			currentReplicas++
		}
	}
	status.CurrentReplicas = currentReplicas
	status.UpdatedReplicas = updatedReplicas

	// Determine the actual and target replica counts
	count := len(replicas)
	targetCount := 0
	if managedSeedSet.DeletionTimestamp == nil {
		targetCount = int(*managedSeedSet.Spec.Replicas)
	}

	// Determine whether scaling out or in
	scalingOut, scalingIn := count < targetCount, count > targetCount

	// Reconcile the pending replica, if any
	if pendingReplica != nil {
		if pending, err := a.reconcileReplica(ctx, log, managedSeedSet, status, pendingReplica, scalingIn, updateRevisionHash); err != nil || pending {
			return status, false, err
		}
	}

	switch {
	case scalingOut:
		// Initialize a new replica and create its shoot
		ordinal := getNextOrdinal(replicas, status)
		if err := a.createReplica(ctx, log, managedSeedSet, status, ordinal, updateRevisionHash); err != nil {
			return status, false, err
		}

		// Increment Replicas and NextReplicaNumber in status
		status.Replicas++
		status.NextReplicaNumber = ordinal + 1

		return status, false, nil

	case scalingIn:
		// Determine the replica to be deleted
		// From all deletable replicas, choose the one with lowest priority
		if len(deletableReplicas) == 0 {
			return status, false, fmt.Errorf("no deletable replicas found")
		}
		sort.Sort(ascendingPriority(deletableReplicas))
		r := deletableReplicas[0]

		// Delete the replica's managed seed (if it exists), or its shoot (if not)
		if err := a.deleteReplica(ctx, log, managedSeedSet, status, r); err != nil {
			return status, false, err
		}

		// Decrement ReadyReplicas in status
		if replicaIsReady(r) {
			status.ReadyReplicas--
		}

		return status, false, nil
	}

	// Reconcile postponed replicas
	for _, r := range postponedReplicas {
		if pending, err := a.reconcileReplica(ctx, log, managedSeedSet, status, r, scalingIn, updateRevisionHash); err != nil || pending {
			return status, false, err
		}
	}

	// Rolling update: if the spec changed (UpdateRevision != CurrentRevision), update one replica at a
	// time starting from the highest ordinal, exactly like the Kubernetes StatefulSet controller.
	if status.UpdateRevision != status.CurrentRevision {
		if pending, err := a.performRollingUpdate(ctx, log, managedSeedSet, status, replicas, updateRevisionHash); err != nil || pending {
			return status, false, err
		}
	}

	log.V(1).Info("Nothing to do")
	status.PendingReplica = nil
	return status, true, nil
}

// Event reason constants.
const (
	EventCreatingShoot                   = "CreatingShoot"
	EventDeletingShoot                   = "DeletingShoot"
	EventUpdatingShoot                   = "UpdatingShoot"
	EventRetryingShootReconciliation     = "RetryingShootReconciliation"
	EventNotRetryingShootReconciliation  = "NotRetryingShootReconciliation"
	EventRetryingShootDeletion           = "RetryingShootDeletion"
	EventNotRetryingShootDeletion        = "NotRetryingShootDeletion"
	EventWaitingForShootReconciled       = "WaitingForShootReconciled"
	EventWaitingForShootDeleted          = "WaitingForShootDeleted"
	EventWaitingForShootHealthy          = "WaitingForShootHealthy"
	EventWaitingForShootUpdated          = "WaitingForShootUpdated"
	EventCreatingManagedSeed             = "CreatingManagedSeed"
	EventDeletingManagedSeed             = "DeletingManagedSeed"
	EventUpdatingManagedSeed             = "UpdatingManagedSeed"
	EventWaitingForManagedSeedRegistered = "WaitingForManagedSeedRegistered"
	EventWaitingForManagedSeedDeleted    = "WaitingForManagedSeedDeleted"
	EventWaitingForManagedSeedUpdated    = "WaitingForManagedSeedUpdated"
	EventWaitingForSeedReady             = "WaitingForSeedReady"
)

func (a *actuator) reconcileReplica(
	ctx context.Context,
	log logr.Logger,
	managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet,
	status *seedmanagementv1alpha1.ManagedSeedSetStatus,
	r Replica,
	scalingIn bool,
	updateRevisionHash string,
) (bool, error) {
	replicaStatus := r.GetStatus()
	log = log.WithValues("replica", r.GetObjectKey())

	// Handle rolling-update phases before falling through to the normal status-based switch.
	// These cases are identified by the PendingReplica reason stored in status and must be
	// checked first because GetStatus() returns ManagedSeed* status codes while the managed
	// seed is still present (even mid-update).
	if status.PendingReplica != nil && status.PendingReplica.Name == r.GetName() {
		switch status.PendingReplica.Reason {
		case seedmanagementv1alpha1.ShootUpdatingReason:
			return a.reconcileShootUpdatePhase(ctx, log, managedSeedSet, status, r, updateRevisionHash)
		case seedmanagementv1alpha1.ManagedSeedUpdatingReason:
			return a.reconcileManagedSeedUpdatePhase(ctx, log, managedSeedSet, status, r)
		}
	}

	switch {
	case replicaStatus == StatusShootReconcileFailed && !scalingIn:
		// This replica's shoot reconciliation has failed, retry it if max retries is not yet reached
		retries := getPendingReplicaRetries(status, r.GetName(), seedmanagementv1alpha1.ShootReconcilingReason)
		if int(retries) < *a.cfg.MaxShootRetries {
			log.Info("Retrying Shoot reconciliation")
			a.infoEventf(managedSeedSet, EventRetryingShootReconciliation, gardencorev1beta1.EventActionReconcile, "Retrying Shoot %s reconciliation", r.GetFullName())
			if err := r.RetryShoot(ctx, a.gardenClient); err != nil {
				return false, err
			}
			updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootReconcilingReason, ptr.To(retries+1))
		} else {
			log.Info("Not retrying Shoot reconciliation since max retries have been reached", "maxRetries", *a.cfg.MaxShootRetries)
			a.infoEventf(managedSeedSet, EventNotRetryingShootReconciliation, gardencorev1beta1.EventActionReconcile, "Not retrying Shoot %s reconciliation since max retries have been reached", r.GetFullName())
			updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootReconcileFailedReason, &retries)
		}
		return true, nil

	case replicaStatus == StatusShootDeleteFailed:
		// This replica's shoot deletion has failed, retry it if max retries is not yet reached
		retries := getPendingReplicaRetries(status, r.GetName(), seedmanagementv1alpha1.ShootDeletingReason)
		if int(retries) < *a.cfg.MaxShootRetries {
			log.Info("Retrying Shoot deletion")
			a.infoEventf(managedSeedSet, EventRetryingShootDeletion, gardencorev1beta1.EventActionDelete, "Retrying Shoot %s deletion", r.GetFullName())
			if err := r.RetryShoot(ctx, a.gardenClient); err != nil {
				return false, err
			}
			updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootDeletingReason, ptr.To(retries+1))
		} else {
			log.Info("Not retrying Shoot deletion since max retries have been reached", "maxRetries", *a.cfg.MaxShootRetries)
			a.infoEventf(managedSeedSet, EventNotRetryingShootDeletion, gardencorev1beta1.EventActionDelete, "Not retrying Shoot %s deletion since max retries have been reached", r.GetFullName())
			updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootDeleteFailedReason, &retries)
		}
		return true, nil

	case replicaStatus == StatusShootReconciling && !scalingIn:
		// This replica's shoot is reconciling, wait for it to be reconciled before moving to the next replica
		log.Info("Waiting for Shoot to be reconciled")
		a.infoEventf(managedSeedSet, EventWaitingForShootReconciled, gardencorev1beta1.EventActionReconcile, "Waiting for Shoot %s to be reconciled", r.GetFullName())
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootReconcilingReason, nil)
		return true, nil

	case replicaStatus == StatusShootDeleting:
		// This replica's shoot is deleting, wait for it to be deleted before moving to the next replica
		log.Info("Waiting for Shoot to be deleted")
		a.infoEventf(managedSeedSet, EventWaitingForShootDeleted, gardencorev1beta1.EventActionDelete, "Waiting for Shoot %s to be deleted", r.GetFullName())
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootDeletingReason, nil)
		return true, nil

	case replicaStatus == StatusShootReconciled:
		// This replica's shoot is fully reconciled and its managed seed doesn't exist
		// If not scaling in, create its managed seed, otherwise delete its shoot
		if !scalingIn {
			log.Info("Creating ManagedSeed")
			a.infoEventf(managedSeedSet, EventCreatingManagedSeed, gardencorev1beta1.EventActionReconcile, "Creating ManagedSeed %s", r.GetFullName())
			if err := r.CreateManagedSeed(ctx, a.gardenClient, updateRevisionHash); err != nil {
				return false, err
			}
			updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ManagedSeedPreparingReason, nil)
		} else {
			log.Info("Deleting Shoot")
			a.infoEventf(managedSeedSet, EventDeletingShoot, gardencorev1beta1.EventActionDelete, "Deleting Shoot %s", r.GetFullName())
			if err := r.DeleteShoot(ctx, a.gardenClient); err != nil {
				return false, err
			}
			updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootDeletingReason, nil)
		}
		return true, nil

	case replicaStatus == StatusManagedSeedPreparing && !scalingIn:
		// This replica's managed seed is preparing, wait for the it to be registered before moving to the next replica
		log.Info("Waiting for ManagedSeed to be registered")
		a.infoEventf(managedSeedSet, EventWaitingForManagedSeedRegistered, gardencorev1beta1.EventActionReconcile, "Waiting for ManagedSeed %s to be registered", r.GetFullName())
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ManagedSeedPreparingReason, nil)
		return true, nil

	case replicaStatus == StatusManagedSeedDeleting:
		// This replica's managed seed is deleting, wait for it to be deleted before moving to the next replica
		log.Info("Waiting for ManagedSeed to be deleted")
		a.infoEventf(managedSeedSet, EventWaitingForManagedSeedDeleted, gardencorev1beta1.EventActionDelete, "Waiting for ManagedSeed %s to be deleted", r.GetFullName())
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ManagedSeedDeletingReason, nil)
		return true, nil

	case !r.IsSeedReady() && !scalingIn:
		// This replica's seed is not ready, wait for it to be ready before moving to the next replica
		log.Info("Waiting for Seed to be ready")
		a.infoEventf(managedSeedSet, EventWaitingForSeedReady, gardencorev1beta1.EventActionReconcile, "Waiting for Seed %s to be ready", r.GetName())
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.SeedNotReadyReason, nil)
		return true, nil

	case r.GetShootHealthStatus() != gardenerutils.ShootStatusHealthy && !scalingIn:
		// This replica's shoot is not healthy, wait for it to be healthy before moving to the next replica
		log.Info("Waiting for Shoot to be healthy")
		a.infoEventf(managedSeedSet, EventWaitingForShootHealthy, gardencorev1beta1.EventActionReconcile, "Waiting for Shoot %s to be healthy", r.GetFullName())
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootNotHealthyReason, nil)
		return true, nil
	}

	return false, nil
}

func (a *actuator) createReplica(
	ctx context.Context,
	log logr.Logger,
	managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet,
	status *seedmanagementv1alpha1.ManagedSeedSetStatus,
	ordinal int32,
	updateRevisionHash string,
) error {
	r := a.replicaFactory.NewReplica(managedSeedSet, nil, nil, nil, false)

	fullName := getFullName(managedSeedSet, ordinal)
	log.Info("Creating Shoot", "replica", client.ObjectKey{Namespace: managedSeedSet.Namespace, Name: fullName})
	a.infoEventf(managedSeedSet, EventCreatingShoot, gardencorev1beta1.EventActionReconcile, "Creating Shoot %s", fullName)
	if err := r.CreateShoot(ctx, a.gardenClient, ordinal, updateRevisionHash); err != nil {
		return err
	}
	updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootReconcilingReason, nil)
	return nil
}

func (a *actuator) deleteReplica(
	ctx context.Context,
	log logr.Logger,
	managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet,
	status *seedmanagementv1alpha1.ManagedSeedSetStatus,
	r Replica,
) error {
	log = log.WithValues("replica", r.GetObjectKey())

	if replicaManagedSeedExists(r.GetStatus()) {
		log.Info("Deleting ManagedSeed")
		a.infoEventf(managedSeedSet, EventDeletingManagedSeed, gardencorev1beta1.EventActionDelete, "Deleting ManagedSeed %s", r.GetFullName())
		if err := r.DeleteManagedSeed(ctx, a.gardenClient); err != nil {
			return err
		}
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ManagedSeedDeletingReason, nil)
	} else {
		log.Info("Deleting Shoot")
		a.infoEventf(managedSeedSet, EventDeletingShoot, gardencorev1beta1.EventActionDelete, "Deleting Shoot %s", r.GetFullName())
		if err := r.DeleteShoot(ctx, a.gardenClient); err != nil {
			return err
		}
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootDeletingReason, nil)
	}
	return nil
}

// reconcileShootUpdatePhase handles the pending state when a replica's shoot has been updated to
// a new revision. It waits for the shoot reconciliation to complete, then triggers the
// ManagedSeed update, advancing the pending reason to ManagedSeedUpdatingReason.
func (a *actuator) reconcileShootUpdatePhase(
	ctx context.Context,
	log logr.Logger,
	managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet,
	status *seedmanagementv1alpha1.ManagedSeedSetStatus,
	r Replica,
	updateRevisionHash string,
) (bool, error) {
	if r.IsShootReconcileSucceeded() {
		// Shoot has been reconciled with the new spec — now update the ManagedSeed.
		log.Info("Shoot reconciled after update, updating ManagedSeed")
		a.infoEventf(managedSeedSet, EventUpdatingManagedSeed, gardencorev1beta1.EventActionReconcile, "Updating ManagedSeed %s to new revision", r.GetFullName())
		if err := r.UpdateManagedSeed(ctx, a.gardenClient, updateRevisionHash); err != nil {
			return false, err
		}
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ManagedSeedUpdatingReason, nil)
		return true, nil
	}
	// Shoot is still reconciling (or failed) — keep waiting.
	log.Info("Waiting for Shoot to be reconciled after update")
	a.infoEventf(managedSeedSet, EventWaitingForShootUpdated, gardencorev1beta1.EventActionReconcile, "Waiting for Shoot %s to be reconciled after update", r.GetFullName())
	updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootUpdatingReason, nil)
	return true, nil
}

// reconcileManagedSeedUpdatePhase handles the pending state when a replica's managed seed has been
// updated to a new revision. It waits for the managed seed to be registered again, then checks
// seed readiness and shoot health before considering the replica fully updated.
func (a *actuator) reconcileManagedSeedUpdatePhase(
	ctx context.Context,
	log logr.Logger,
	managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet,
	status *seedmanagementv1alpha1.ManagedSeedSetStatus,
	r Replica,
) (bool, error) {
	// GetStatus() returns StatusManagedSeedRegistered only when managedSeedRegistered() is true.
	if r.GetStatus() != StatusManagedSeedRegistered {
		log.Info("Waiting for ManagedSeed to be registered after update")
		a.infoEventf(managedSeedSet, EventWaitingForManagedSeedUpdated, gardencorev1beta1.EventActionReconcile, "Waiting for ManagedSeed %s to be registered after update", r.GetFullName())
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ManagedSeedUpdatingReason, nil)
		return true, nil
	}
	if !r.IsSeedReady() {
		log.Info("Waiting for Seed to be ready after ManagedSeed update")
		a.infoEventf(managedSeedSet, EventWaitingForSeedReady, gardencorev1beta1.EventActionReconcile, "Waiting for Seed %s to be ready after update", r.GetName())
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.SeedNotReadyReason, nil)
		return true, nil
	}
	if r.GetShootHealthStatus() != gardenerutils.ShootStatusHealthy {
		log.Info("Waiting for Shoot to be healthy after ManagedSeed update")
		a.infoEventf(managedSeedSet, EventWaitingForShootHealthy, gardencorev1beta1.EventActionReconcile, "Waiting for Shoot %s to be healthy after update", r.GetFullName())
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootNotHealthyReason, nil)
		return true, nil
	}
	// Replica is fully updated and healthy — clear the pending state.
	return false, nil
}

// performRollingUpdate implements the rolling-update strategy: starting from the replica with the
// highest ordinal (subject to the UpdateStrategy.Partition setting), it updates one replica per
// reconciliation cycle, waiting for each one to become healthy before moving to the next.
// When all replicas at or above the partition are on the update revision, CurrentRevision is
// advanced to UpdateRevision.
func (a *actuator) performRollingUpdate(
	ctx context.Context,
	log logr.Logger,
	managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet,
	status *seedmanagementv1alpha1.ManagedSeedSetStatus,
	replicas []Replica,
	updateRevisionHash string,
) (bool, error) {
	partition := getPartition(managedSeedSet)

	// Work from highest to lowest ordinal (reverse of the sorted-ascending slice).
	for i := len(replicas) - 1; i >= 0; i-- {
		r := replicas[i]
		if r.GetOrdinal() < partition {
			// Replicas below the partition are intentionally kept on the current revision.
			break
		}
		if r.GetRevisionHash() == updateRevisionHash {
			// This replica is already on the update revision; only one replica is updated
			// at a time — if it is not yet ready, wait before touching the next one.
			if !replicaIsReady(r) {
				log.V(1).Info("Waiting for already-updated replica to become ready before proceeding", "replica", r.GetObjectKey())
				return true, nil
			}
			continue
		}

		// This replica needs to be updated. Update its shoot first; the subsequent phases
		// (ManagedSeed update, readiness checks) are handled by reconcileShootUpdatePhase /
		// reconcileManagedSeedUpdatePhase once the pending reason is set.
		log.Info("Updating Shoot for rolling update", "replica", r.GetObjectKey())
		a.infoEventf(managedSeedSet, EventUpdatingShoot, gardencorev1beta1.EventActionReconcile, "Updating Shoot %s to revision %s", r.GetFullName(), strings.TrimPrefix(status.UpdateRevision, managedSeedSet.Name+"-"))
		if err := r.UpdateShoot(ctx, a.gardenClient, updateRevisionHash); err != nil {
			return false, err
		}
		updatePendingReplica(status, r.GetName(), seedmanagementv1alpha1.ShootUpdatingReason, nil)
		return true, nil
	}

	// All replicas at or above the partition are on the update revision — the rolling update
	// is complete; advance CurrentRevision so getMSSRevisions treats this as stable.
	log.Info("Rolling update complete, advancing CurrentRevision", "revision", status.UpdateRevision)
	status.CurrentRevision = status.UpdateRevision
	return false, nil
}

// getPartition returns the configured update partition for the managed seed set.
// Replicas with ordinal < partition are not updated during a rolling update.
func getPartition(managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet) int32 {
	if managedSeedSet.Spec.UpdateStrategy != nil &&
		managedSeedSet.Spec.UpdateStrategy.RollingUpdate != nil &&
		managedSeedSet.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
		return *managedSeedSet.Spec.UpdateStrategy.RollingUpdate.Partition
	}
	return 0
}

func (a *actuator) infoEventf(managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet, reason, action, fmt string, args ...any) {	a.recorder.Eventf(managedSeedSet, nil, corev1.EventTypeNormal, reason, action, fmt, args...)
}

func (a *actuator) errorEventf(managedSeedSet *seedmanagementv1alpha1.ManagedSeedSet, reason, action, fmt string, args ...any) {
	a.recorder.Eventf(managedSeedSet, nil, corev1.EventTypeWarning, reason, action, fmt, args...)
}

func getPendingReplica(replicas []Replica, status *seedmanagementv1alpha1.ManagedSeedSetStatus) Replica {
	if status.PendingReplica == nil {
		return nil
	}
	for _, r := range replicas {
		if r.GetName() == status.PendingReplica.Name {
			return r
		}
	}
	return nil
}

func getPendingReplicaRetries(status *seedmanagementv1alpha1.ManagedSeedSetStatus, name string, reason seedmanagementv1alpha1.PendingReplicaReason) int32 {
	if status.PendingReplica != nil && status.PendingReplica.Name == name && status.PendingReplica.Reason == reason && status.PendingReplica.Retries != nil {
		return *status.PendingReplica.Retries
	}
	return 0
}

func updatePendingReplica(status *seedmanagementv1alpha1.ManagedSeedSetStatus, name string, reason seedmanagementv1alpha1.PendingReplicaReason, retries *int32) {
	if status.PendingReplica == nil || status.PendingReplica.Name != name || status.PendingReplica.Reason != reason || !reflect.DeepEqual(status.PendingReplica.Retries, retries) {
		status.PendingReplica = &seedmanagementv1alpha1.PendingReplica{
			Name:    name,
			Reason:  reason,
			Since:   Now(),
			Retries: retries,
		}
	}
}

func getNextOrdinal(replicas []Replica, status *seedmanagementv1alpha1.ManagedSeedSetStatus) int32 {
	// Replicas are sorted by ordinal, so the ordinal of the last replica is also the largest one
	if len(replicas) > 0 {
		if nextOrdinal := replicas[len(replicas)-1].GetOrdinal() + 1; nextOrdinal > status.NextReplicaNumber {
			return nextOrdinal
		}
	}
	return status.NextReplicaNumber
}

func replicaIsReady(r Replica) bool {
	return r.GetStatus() == StatusManagedSeedRegistered && r.IsSeedReady() && r.GetShootHealthStatus() == gardenerutils.ShootStatusHealthy
}

func debugReplica(r Replica, log logr.Logger) {
	log.Info("Replica", "objectKey", r.GetObjectKey(), "status", r.GetStatus().String(), "seedReady", r.IsSeedReady(), "shootHealthStatus", r.GetShootHealthStatus())
}

func replicaManagedSeedExists(status ReplicaStatus) bool {
	return status >= StatusManagedSeedPreparing
}

// ascendingOrdinal is a sort.Interface that sorts a list of replicas based on their ordinals.
// Replicas that have not been created by a ManagedSeedSet have an ordinal of -1, and are therefore pushed
// to the front of the list.
type ascendingOrdinal []Replica

func (ao ascendingOrdinal) Len() int {
	return len(ao)
}

func (ao ascendingOrdinal) Swap(i, j int) {
	ao[i], ao[j] = ao[j], ao[i]
}

func (ao ascendingOrdinal) Less(i, j int) bool {
	return ao[i].GetOrdinal() < ao[j].GetOrdinal()
}

// ascendingPriority is a sort.Interface that sorts a list of replicas based on their priority.
type ascendingPriority []Replica

func (ap ascendingPriority) Len() int {
	return len(ap)
}

func (ap ascendingPriority) Swap(i, j int) {
	ap[i], ap[j] = ap[j], ap[i]
}

func (ap ascendingPriority) Less(i, j int) bool {
	// First compare replica statuses
	// Replicas with "less advanced" status are considered lower priority
	if vi, vj := ap[i].GetStatus(), ap[j].GetStatus(); vi != vj {
		return vi < vj
	}

	// Then, compare replica seed readiness
	// Replicas with non-ready seeds are considered lower priority
	if vi, vj := ap[i].IsSeedReady(), ap[j].IsSeedReady(); vi != vj {
		return !vi
	}

	// Then, compare replica shoot health statuses
	// Replicas with "worse" status are considered lower priority
	if vi, vj := gardenerutils.ShootStatusValue(ap[i].GetShootHealthStatus()), gardenerutils.ShootStatusValue(ap[j].GetShootHealthStatus()); vi != vj {
		return vi < vj
	}

	// Finally, compare replica ordinals
	// Replicas with lower ordinals are considered lower priority
	return ap[i].GetOrdinal() < ap[j].GetOrdinal()
}
