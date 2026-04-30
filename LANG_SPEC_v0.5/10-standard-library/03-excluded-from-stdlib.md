### 10.3 Excluded from stdlib

Asymmetric cryptography (RSA, Ed25519, etc.), compression formats other
than gzip (zstd, brotli, lz4), OS-specific APIs (systemd, Windows
registry, etc.; portable credential storage is exposed through
`std.keychain` / `std.secrets`), database drivers, message queue clients, serialization
formats other than JSON/CSV/TSV table text (protobuf, msgpack, avro) — obtained from
community packages or Go FFI.
