package container

// SecretsEnvFilePath is where the daemon drops a dotenv copy of the
// tenant's "compose"-delivery secrets inside the LXC. Apps running
// directly in the LXC get secrets from the Incus config environment;
// nested docker / docker-compose apps do NOT, so they reference this
// file via `env_file:`. See #491.
//
// It lives under /run (tmpfs on systemd distros) so it is evicted on
// container stop and never lands on the writable rootfs — unlike
// otel.env. The compose runtime (root) reads it at container-create
// time and injects the values as env into the app container; the app
// process never reads the file itself, so mode 0400 root is both safe
// and sufficient.
