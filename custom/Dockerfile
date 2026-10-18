# Final backend image stage.
#
# The application code lives in ../surfsense_backend, which is built first:
#   docker build -t soloclef-backend-src:latest --target production ../surfsense_backend
# then this image is built on top of it. The stage exists so a deployment
# can add host-specific packages or patches without forking the application
# image. By default it adds nothing.
ARG BACKEND_BASE=soloclef-backend-src:latest
FROM ${BACKEND_BASE}
