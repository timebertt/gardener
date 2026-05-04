// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package managedseedset

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apirand "k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
)

const (
	// controllerRevisionHashLabel is the label key used to store the hash of a ControllerRevision's Data.
	controllerRevisionHashLabel = "controller.kubernetes.io/hash"
	// defaultRevisionHistoryLimit is the default number of ControllerRevisions to retain.
	defaultRevisionHistoryLimit = 10
)

// mssRevisionPatch is the structure stored as ControllerRevision data.
// It captures the parts of ManagedSeedSetSpec that define the desired templates.
type mssRevisionPatch struct {
	Spec mssRevisionPatchSpec `json:"spec"`
}

type mssRevisionPatchSpec struct {
	Template      interface{} `json:"template"`
	ShootTemplate interface{} `json:"shootTemplate"`
}

// getMSSPatch returns the JSON-encoded patch for the given ManagedSeedSet's template spec.
// Only spec.template and spec.shootTemplate are captured because those are the parts that
// drive rolling updates (analogous to spec.template in StatefulSet).
func getMSSPatch(set *seedmanagementv1alpha1.ManagedSeedSet) ([]byte, error) {
	patch := mssRevisionPatch{
		Spec: mssRevisionPatchSpec{
			Template:      set.Spec.Template,
			ShootTemplate: set.Spec.ShootTemplate,
		},
	}
	return json.Marshal(patch)
}

// hashRevisionData returns a safe-encoded FNV-32a hash of raw, optionally mixed with collisionCount.
// This matches the algorithm used by the Kubernetes StatefulSet controller.
func hashRevisionData(raw []byte, collisionCount *int32) string {
	hf := fnv.New32a()
	_, _ = hf.Write(raw)
	if collisionCount != nil {
		_, _ = hf.Write([]byte(strconv.FormatInt(int64(*collisionCount), 10)))
	}
	return apirand.SafeEncodeString(fmt.Sprint(hf.Sum32()))
}

// controllerRevisionName constructs a ControllerRevision name as "<prefix>-<hash>", truncating
// prefix to 223 bytes to keep the total name within the 253-byte Kubernetes name limit.
func controllerRevisionName(prefix, hash string) string {
	if len(prefix) > 223 {
		prefix = prefix[:223]
	}
	return fmt.Sprintf("%s-%s", prefix, hash)
}

// newMSSRevision creates a ControllerRevision that captures the current template state of set.
// revisionNum is the logical revision number and collisionCount is used to disambiguate names.
func newMSSRevision(set *seedmanagementv1alpha1.ManagedSeedSet, revisionNum int64, collisionCount *int32) (*appsv1.ControllerRevision, error) {
	patch, err := getMSSPatch(set)
	if err != nil {
		return nil, fmt.Errorf("failed to compute ManagedSeedSet revision patch: %w", err)
	}

	hash := hashRevisionData(patch, collisionCount)
	name := controllerRevisionName(set.Name, hash)

	// Carry selector labels onto the ControllerRevision so we can list them by selector later.
	labelMap := make(map[string]string, len(set.Spec.Selector.MatchLabels)+1)
	for k, v := range set.Spec.Selector.MatchLabels {
		labelMap[k] = v
	}
	labelMap[controllerRevisionHashLabel] = hash

	cr := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: set.Namespace,
			Labels:    labelMap,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(set, seedmanagementv1alpha1.SchemeGroupVersion.WithKind("ManagedSeedSet")),
			},
		},
		Data:     runtime.RawExtension{Raw: patch},
		Revision: revisionNum,
	}
	return cr, nil
}

// listRevisions returns all ControllerRevisions owned by set, sorted by ascending Revision number.
func listRevisions(ctx context.Context, c client.Client, set *seedmanagementv1alpha1.ManagedSeedSet) ([]*appsv1.ControllerRevision, error) {
	selector, err := metav1.LabelSelectorAsSelector(&set.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to build label selector for ManagedSeedSet %s: %w", client.ObjectKeyFromObject(set), err)
	}

	list := &appsv1.ControllerRevisionList{}
	if err := c.List(ctx, list, client.InNamespace(set.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("failed to list ControllerRevisions for ManagedSeedSet %s: %w", client.ObjectKeyFromObject(set), err)
	}

	var owned []*appsv1.ControllerRevision
	for i := range list.Items {
		ref := metav1.GetControllerOf(&list.Items[i])
		if ref != nil && ref.UID == set.UID {
			owned = append(owned, &list.Items[i])
		}
	}
	sortControllerRevisions(owned)
	return owned, nil
}

// nextRevisionNumber returns the next (highest+1) revision number given a sorted list of revisions.
// If the list is empty, it returns 1.
func nextRevisionNumber(revisions []*appsv1.ControllerRevision) int64 {
	if len(revisions) == 0 {
		return 1
	}
	return revisions[len(revisions)-1].Revision + 1
}

// equalRevisionData reports whether two ControllerRevisions have identical raw data.
func equalRevisionData(a, b *appsv1.ControllerRevision) bool {
	return bytes.Equal(a.Data.Raw, b.Data.Raw)
}

// findEqualRevisions returns all revisions whose data equals needle's data.
func findEqualRevisions(revisions []*appsv1.ControllerRevision, needle *appsv1.ControllerRevision) []*appsv1.ControllerRevision {
	var result []*appsv1.ControllerRevision
	for _, r := range revisions {
		if equalRevisionData(r, needle) {
			result = append(result, r)
		}
	}
	return result
}

// sortControllerRevisions sorts revisions by ascending Revision, breaking ties by creation
// timestamp (newer first) and then by name.
func sortControllerRevisions(revisions []*appsv1.ControllerRevision) {
	sort.Stable(byRevision(revisions))
}

type byRevision []*appsv1.ControllerRevision

func (b byRevision) Len() int      { return len(b) }
func (b byRevision) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byRevision) Less(i, j int) bool {
	if b[i].Revision == b[j].Revision {
		if b[j].CreationTimestamp.Equal(&b[i].CreationTimestamp) {
			return b[i].Name < b[j].Name
		}
		return b[j].CreationTimestamp.After(b[i].CreationTimestamp.Time)
	}
	return b[i].Revision < b[j].Revision
}

// createControllerRevision creates a new ControllerRevision for set. If a name collision is detected
// (IsAlreadyExists) it increments collisionCount and retries with a new hash.
func createControllerRevision(ctx context.Context, c client.Client, set *seedmanagementv1alpha1.ManagedSeedSet, revision *appsv1.ControllerRevision, collisionCount *int32) (*appsv1.ControllerRevision, error) {
	for {
		hash := hashRevisionData(revision.Data.Raw, collisionCount)
		clone := revision.DeepCopy()
		clone.Name = controllerRevisionName(set.Name, hash)
		clone.Labels[controllerRevisionHashLabel] = hash

		if err := c.Create(ctx, clone); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return nil, err
			}
			// Fetch the existing revision with the same name
			existing := &appsv1.ControllerRevision{}
			if getErr := c.Get(ctx, client.ObjectKey{Namespace: clone.Namespace, Name: clone.Name}, existing); getErr != nil {
				return nil, getErr
			}
			// If the data is identical, the revision already exists – reuse it
			if bytes.Equal(existing.Data.Raw, clone.Data.Raw) {
				return existing, nil
			}
			// True collision: different data under the same name – increment and retry
			*collisionCount++
			continue
		}
		return clone, nil
	}
}

// truncateRevisionHistory deletes surplus ControllerRevisions, keeping at most
// revisionHistoryLimit non-live ones. revisions must be sorted ascending.
func truncateRevisionHistory(ctx context.Context, c client.Client, set *seedmanagementv1alpha1.ManagedSeedSet, revisions []*appsv1.ControllerRevision, liveNames ...string) error {
	limit := defaultRevisionHistoryLimit
	if set.Spec.RevisionHistoryLimit != nil {
		limit = int(*set.Spec.RevisionHistoryLimit)
	}

	live := make(map[string]bool, len(liveNames))
	for _, n := range liveNames {
		if n != "" {
			live[n] = true
		}
	}

	var historic []*appsv1.ControllerRevision
	for _, r := range revisions {
		if !live[r.Name] {
			historic = append(historic, r)
		}
	}

	toDelete := len(historic) - limit
	for i := 0; i < toDelete; i++ {
		if err := client.IgnoreNotFound(c.Delete(ctx, historic[i])); err != nil {
			return fmt.Errorf("failed to delete old ControllerRevision %s: %w", historic[i].Name, err)
		}
	}
	return nil
}

// revisionHashFromName extracts the controller-revision hash from a ControllerRevision name.
// ControllerRevision names have the format "<setName>-<hash>", where the hash (from SafeEncodeString)
// is purely alphanumeric and never contains dashes.
func revisionHashFromName(revisionName, setName string) string {
	prefix := setName
	if len(prefix) > 223 {
		prefix = prefix[:223]
	}
	prefix += "-"
	if strings.HasPrefix(revisionName, prefix) {
		return revisionName[len(prefix):]
	}
	return ""
}

// getMSSRevisions computes the current and update ControllerRevisions for set. It mirrors
// the getStatefulSetRevisions logic from the Kubernetes StatefulSet controller:
//
//  1. Build a candidate updateRevision from the current spec templates.
//  2. Search the existing revision history for an equal entry.
//  3. If an equal entry is found at the end of the list (most recent) → reuse it (no change).
//  4. If an equal entry is found but not at the end            → rollback: bump its .Revision
//     number so it becomes the latest, triggering a roll-forward to the old state.
//  5. If no equal entry is found                              → create a new revision.
//  6. Determine currentRevision from status.CurrentRevision; fall back to updateRevision.
//  7. Prune old revisions beyond RevisionHistoryLimit.
//
// The function updates status.CollisionCount, status.CurrentRevision, and status.UpdateRevision.
func getMSSRevisions(
	ctx context.Context,
	c client.Client,
	set *seedmanagementv1alpha1.ManagedSeedSet,
	status *seedmanagementv1alpha1.ManagedSeedSetStatus,
) error {
	revisions, err := listRevisions(ctx, c, set)
	if err != nil {
		return err
	}

	// Use a local copy of CollisionCount to avoid modifying status until we are done.
	var collisionCount int32
	if status.CollisionCount != nil {
		collisionCount = *status.CollisionCount
	}

	// Build a candidate revision from the current spec.
	candidate, err := newMSSRevision(set, nextRevisionNumber(revisions), &collisionCount)
	if err != nil {
		return err
	}

	revisionCount := len(revisions)
	equalRevisions := findEqualRevisions(revisions, candidate)
	equalCount := len(equalRevisions)

	var updateRevision *appsv1.ControllerRevision

	switch {
	case equalCount > 0 && equalRevisionData(revisions[revisionCount-1], equalRevisions[equalCount-1]):
		// The latest existing revision is already equal → no change needed.
		updateRevision = revisions[revisionCount-1]

	case equalCount > 0:
		// An equal revision exists somewhere in history but is not the latest.
		// This is the rollback case: bump the revision number of that entry so it
		// sorts to the top and becomes the new update target.
		target := equalRevisions[equalCount-1].DeepCopy()
		target.Revision = nextRevisionNumber(revisions)
		if err := c.Update(ctx, target); err != nil {
			return fmt.Errorf("failed to update ControllerRevision %s for rollback: %w", target.Name, err)
		}
		updateRevision = target

	default:
		// No equal revision exists – create a brand-new one (with collision handling).
		updateRevision, err = createControllerRevision(ctx, c, set, candidate, &collisionCount)
		if err != nil {
			return fmt.Errorf("failed to create ControllerRevision for ManagedSeedSet %s: %w", client.ObjectKeyFromObject(set), err)
		}
		revisions = append(revisions, updateRevision)
	}

	// Persist the (possibly updated) collision count back to status.
	if collisionCount != 0 || status.CollisionCount != nil {
		status.CollisionCount = &collisionCount
	}

	// Determine the currentRevision: the one matching the name stored in status, or –
	// on first reconcile or after full rollout – the updateRevision itself.
	var currentRevision *appsv1.ControllerRevision
	for _, r := range revisions {
		if r.Name == status.CurrentRevision {
			currentRevision = r
			break
		}
	}
	if currentRevision == nil {
		currentRevision = updateRevision
	}

	status.CurrentRevision = currentRevision.Name
	status.UpdateRevision = updateRevision.Name

	return truncateRevisionHistory(ctx, c, set, revisions, currentRevision.Name, updateRevision.Name)
}
