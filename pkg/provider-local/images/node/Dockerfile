FROM kindest/node:v1.21.1

ARG TARGETOS
ARG TARGETARCH

RUN apt-get update -yq && \
    apt-get install -yq --no-install-recommends wget

# cloud-config-downloader still needs the docker cli to be present in the image for preloading the hyperkube image
# see https://github.com/gardener/gardener/issues/4673
# install nerdctl as a (mostly) docker-compatible replacement and fool the cloud-config-downloader with a small wrapper
# this is quite hacky but releives us from installing docker here
ARG NERDCTL_VERSION=0.13.0
RUN curl -Lo /tmp/nerdctl.tar.gz https://github.com/containerd/nerdctl/releases/download/v$NERDCTL_VERSION/nerdctl-$NERDCTL_VERSION-$TARGETOS-$TARGETARCH.tar.gz && \
    tar Cxzvvf /usr/local/bin /tmp/nerdctl.tar.gz && \
    rm -f /tmp/nerdctl.tar.gz

# add our small nerdctl wrapper and disguise it as docker
COPY docker /usr/bin/docker
RUN chmod +x /usr/bin/docker

# remove kind kubelet unit
RUN rm -f /etc/systemd/system/kubelet.service && \
    rm -rf /etc/systemd/system/kubelet.service.d/

# add our userdata executor unit
COPY run-userdata.sh /run-userdata.sh
COPY run-userdata.service /etc/systemd/system
RUN systemctl enable run-userdata.service

ENTRYPOINT ["/usr/local/bin/entrypoint", "/sbin/init"]
