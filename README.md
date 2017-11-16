OpenShift Web Console Server
============================

The API server for the [OpenShift web console](https://github.com/openshift/origin-web-console), part of the
[OpenShift](https://github.com/openshift/origin) application platform.

[![Build Status](https://travis-ci.org/openshift/origin-web-console-server.svg?branch=master)](https://travis-ci.org/openshift/origin-web-console-server)

**Under Construction**

The web console server runs as a pod on the platform. The OpenShift master
proxies requests from the web console context root, typically `/console/`, to
the server running in the pod. The pod then serves the static HTML, JavaScript,
and CSS files that make up the console.

The web console assets themselves are developed in the
[origin-web-console](https://github.com/openshift/origin-web-console)
repository. They are included in the web console server binary using
[go-bindata](https://github.com/jteeuwen/go-bindata).

Building
--------

To build the binary, run

```
$ make
```

To build the RPM and images, run

```
$ make build-images
```

Note: You must be on a Linux system for `make build-images` to work since it
builds an RPM and then an image from the RPM.

Updating Go Tooling
-------------------

See https://github.com/openshift/release/tree/master/tools/hack/golang for
instructions on how to update the Go tooling used by this project.
