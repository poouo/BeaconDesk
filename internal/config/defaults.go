package config

const DefaultRelayConfigText = `# BeaconDesk connection server configuration.
# For production, configure tls_cert_file/tls_key_file and set
# allow_insecure_plaintext to false.
listen = ":8443"
transport = "websocket"
websocket_path = "/ws"
web_control_enabled = true
web_control_path = "/web"
web_listen = ":8080"
shared_token = "change-me"
# tls_cert_file = "/etc/beacondesk/tls/fullchain.pem"
# tls_key_file = "/etc/beacondesk/tls/privkey.pem"
heartbeat_timeout_seconds = 45
max_clients = 1000
bandwidth_limit_kbps = 4096
allow_insecure_plaintext = true
`
