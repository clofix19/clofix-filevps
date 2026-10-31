# This isolated test daemon has no host Docker socket or customer data mounts.
FROM docker:27-dind
RUN apk add --no-cache e2fsprogs util-linux
COPY database.test /usr/local/bin/database.test
