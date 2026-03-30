# Service deployment

This repo deploys the Go notification, tracking, and dispatch services to the existing Kubernetes cluster in the `truckmoov` namespace.

## Kubernetes manifests

Kustomize bundles are provided under:

- `deploy/k8s/notification`
- `deploy/k8s/tracking`
- `deploy/k8s/dispatch`

Each bundle defines:

- a `Service`
- a `Deployment`
- a `ConfigMap`
- an optional `Secret` contract via `secret.example.yaml`

## Service names

These manifests expose the same in-cluster service names already referenced by the platform gateway:

- `notification-service:3003`
- `tracking-service:3004`
- `dispatch-service:3005`

## Runtime notes

Dispatch now persists jobs through Postgres so business records survive restarts and can be queried relationally. Tracking still uses Redis for low-latency location state. The manifests default to `replicas: 2`, assuming a reachable shared Postgres service for dispatch and Redis service for tracking.

## Image names

The kustomize bundles expect these image names:

- `truckmoov-notification`
- `truckmoov-tracking`
- `truckmoov-dispatch`
