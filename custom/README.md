# Deployment backend image

`Dockerfile` is the final stage of the backend image. It is built from the
application source image:

```bash
docker build -t soloclef-backend-src:latest --target production surfsense_backend
docker build -t soloclef-backend-fast:latest custom
```

By default this stage adds nothing. Put host-specific packages or patches
here if you need them, and keep the application code in `surfsense_backend`.
