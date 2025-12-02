FROM ubuntu:24.04

ENV OPERATOR=/usr/local/bin/cluster-orchestrator \
    USER_UID=root \
    USER_NAME=discovery
COPY _output/bin/cluster-orchestrator ${OPERATOR}

ENTRYPOINT ["/usr/local/bin/cluster-orchestrator"]

USER ${USER_UID}