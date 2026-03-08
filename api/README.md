# api/ (Go services)

This folder contains protobuf definitions for internal gRPC APIs.

## Rules
- Keep protobufs under `api/proto/<service>/vN/`.
- Use `common/v1` for shared messages (IDs, coordinates, timestamps).
- Do not expose protobufs directly to the browser; external APIs remain REST via Kong.

## Generation (example)
Add a `make proto` target later, e.g.
- `buf generate` (preferred) OR `protoc` commands.

Initial scaffold should include:
- tracking/v1/tracking.proto
- dispatch/v1/dispatch.proto
- common/v1/common.proto
