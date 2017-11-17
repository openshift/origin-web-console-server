#
# This is the integrated Origin Web Console Server. It is configured to
# publish metadata to OpenShift to provide automatic management of images on push.
#
# The standard name for this image is openshift/origin-docker-registry
#
FROM openshift/origin-base

RUN INSTALL_PKGS="origin-web-console" && \
    yum --enablerepo=origin-local-release install -y ${INSTALL_PKGS} && \
    rpm -V ${INSTALL_PKGS} && \
    yum clean all

LABEL io.k8s.display-name="OpenShift Web Console" \
      io.k8s.description="This is a component of OpenShift Container Platform and provides a web console." \
      io.openshift.tags="openshift"

# doesn't require a root user.
USER 1001
EXPOSE 5000

CMD [ "/usr/bin/origin-web-console" ]
