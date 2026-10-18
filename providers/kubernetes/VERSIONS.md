# Kubernetes provider: version assumptions

A minimal REST client for the Kubernetes API (no `client-go`, ADR 0008),
used by every Kubernetes-facing provider.

| What | Assumed | Where |
|---|---|---|
| Kubeconfig | client certificate or bearer token, one cluster, one user | `config.go` |
| REST | core `v1` and any group/version a caller names by path; server-side apply (`application/apply-patch+yaml`, field manager `serendip`) | `client.go` |
| Service proxy | `/api/v1/namespaces/{ns}/services/{name}:{port}/proxy` | `client.go` `ProxyURL` |
| Port-forward | the kubelet's WebSocket channel protocol, subprotocol `v4.channel.k8s.io` | `websocket.go`, `portforward.go` |

## The Service proxy strips `Authorization`

The API server removes the `Authorization` header from proxied requests:
it authenticates the caller to the API server itself. A cluster service
that needs a bearer token of its own can't be reached that way. That
covers ThunderID's management API (spec 016 T010, spec 018 T001: the same
token got 401 through the proxy and 200 through a port-forward).
OpenBao's `X-Vault-Token` header passes, so OpenBao keeps using the
proxy.

## Port-forward (spec 018, ADR 0015)

- **Endpoint:** `GET /api/v1/namespaces/{ns}/pods/{pod}/portforward?ports=N`,
  upgraded to a WebSocket with subprotocol `v4.channel.k8s.io`.
  - Every message is one channel byte plus payload. For the single
    forwarded port, channel 0 is data both ways and channel 1 is error
    text from the server.
  - The first message on each channel is the port number, a 2-byte
    little-endian `uint16`, which is discarded.
  - One WebSocket is one TCP connection.
- **Verified on** kind (kindest/node v1.34.0) and K3s v1.36.4+k3s1, our
  `agent/bootstrap` pin (spec 018 T001).
  - This mode doesn't depend on the `PortForwardWebsockets` feature gate,
    which governs KEP-4006's SPDY-tunnelling mode: beta and on by default
    since 1.31, GA in 1.38. That's inferred from upstream; both verified
    versions have the gate on anyway.
  - The SPDY-tunnelling mode is not implemented: it needs SPDY/3.1.
- **The client side:**
  - It checks the handshake: the 101 status, `Upgrade`,
    `Sec-WebSocket-Accept` and the echoed subprotocol.
  - Frames are masked. Server messages are bounded at 1 MiB and
    error-channel text at 4 KiB.
  - A bearer token is refused over plain `http` to a non-loopback host.
  - API-server refusals are shortened to the status and a quoted, cut
    `Status.message`.
- **RBAC:** `create` on `pods/portforward` in the target namespace, plus
  `get` on the Service and `list` on Pods (`ReadyPod`).
- **Closing:** a close with unread incoming data can make the peer see a
  reset rather than the close frame (Windows). That's harmless:
  `http.Transport` drops the connection either way.
