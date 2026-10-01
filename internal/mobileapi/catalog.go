package mobileapi

import (
	"strings"

	"github.com/Live-yum/iotools/internal/config"
)

type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
	Hint  string `json:"hint,omitempty"`
}
type Action struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Mutates  bool           `json:"mutates"`
	Defaults map[string]any `json:"defaults"`
}
type Protocol struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Actions []Action `json:"actions"`
	Fields  []Field  `json:"fields"`
}

var protocolActions = map[string]string{
	"http":   "GET HEAD OPTIONS POST PUT PATCH DELETE TRACE CONNECT",
	"mqtt":   "subscribe read-one publish preview-retained clean-retained",
	"kafka":  "topics brokers groups group lag offsets topic-config consume produce create-topic delete-topic alter-topic expand-partitions delete-group schemas schema schema-versions register-schema delete-subject purge-subject delete-schema connectors connector update-connector pause-connector resume-connector delete-connector",
	"modbus": "read-holding read-input read-coils read-discrete write-register write-registers write-coil write-coils write-typed read-write-registers read-device-id scan-units sweep-holding sweep-input sweep-coils sweep-discrete search-holding read-raw write-raw",
	"opcua":  "discover browse references attributes read write call subscribe browse-path node-path method-arguments",
}
var protocolFields = map[string]string{
	"http":   "headers query body json form_urlencoded form_multipart body_file body_stream response_file max_upload_bytes max_response_bytes query_filter bearer username password ca_file cert_file key_file follow_redirects max_redirects redirect_origins ignore_certificate_hosts persist crypto request_crypto request_transforms response_transform",
	"mqtt":   "topic topics qos payload payload_encoding retain client_id username password ca_file cert_file key_file limit ignore_retained auto_reconnect reconnect_interval_ms scan_duration_ms max_topics confirm_topics confirm_token",
	"kafka":  "topic group groups partition consume_partitions offset start_time limit filter key_filter key_prefix value_prefix key value partitions replication_factor configs tls ca_file cert_file key_file sasl username password subject version confirm_subject connector json headers body bearer key_format value_format key_subject value_subject key_version value_version schema_registry_url schema_registry_username schema_registry_password schema_registry_bearer schema_registry_ca_file schema_registry_cert_file schema_registry_key_file",
	"modbus": "unit address count value values value_type word_order samples interval_ms read_address read_count units end_address match_value pdu_hex read_code object_id scan_type sweep_cycles sweep_recover stop_first connect_timeout_ms request_timeout_ms request_gap_ms baud data_bits parity stop_bits pins labels rules columns keymap matrix_columns write_log_file write_log_previous next_config",
	"opcua":  "node_id node_ids browse_path object_id method_id value value_type arguments attribute attributes direction reference_type include_subtypes max_references max_events interval_ms auto_reconnect reconnect_interval_ms security_policy security_mode server_cert_sha256 allow_insecure allow_legacy_security auth username password ca_file cert_file key_file auth_cert_file auth_key_file",
}
var jsonFields = "headers query json form_urlencoded form_multipart crypto request_transforms response_transform topics confirm_topics groups consume_partitions configs values units pins labels rules columns keymap write_log_previous node_ids arguments attributes redirect_origins ignore_certificate_hosts"
var booleanFields = "retain ignore_retained auto_reconnect tls follow_redirects persist sweep_recover stop_first include_subtypes allow_insecure allow_legacy_security"
var numberFields = "qos limit reconnect_interval_ms scan_duration_ms max_topics partition partitions replication_factor unit address count samples interval_ms read_address read_count end_address match_value read_code object_id sweep_cycles connect_timeout_ms request_timeout_ms request_gap_ms baud data_bits stop_bits matrix_columns max_references max_events max_redirects max_upload_bytes max_response_bytes"

func containsWord(list, key string) bool { return strings.Contains(" "+list+" ", " "+key+" ") }
func knownAction(protocol, action string) bool {
	if protocol == "http" {
		action = strings.ToUpper(action)
	}
	return containsWord(protocolActions[protocol], action)
}

// Catalog is a complete, offline list of protocol operations and editable
// engine parameters. Form fields retain engine names for source round trips.
func Catalog() map[string]any {
	protocols := []Protocol{}
	for _, id := range []string{"http", "mqtt", "kafka", "modbus", "opcua"} {
		names := map[string]string{"http": "HTTP", "mqtt": "MQTT", "kafka": "Kafka", "modbus": "Modbus", "opcua": "OPC UA"}
		p := Protocol{ID: id, Name: names[id], Actions: []Action{}, Fields: []Field{}}
		for _, action := range strings.Fields(protocolActions[id]) {
			p.Actions = append(p.Actions, Action{ID: action, Name: strings.ReplaceAll(action, "-", " "), Mutates: (config.Request{Protocol: id, Action: action}).Mutates(), Defaults: actionDefaults(id, action)})
		}
		for _, key := range strings.Fields(protocolFields[id]) {
			kind := "text"
			if containsWord(jsonFields, key) {
				kind = "json"
			}
			if containsWord(numberFields, key) {
				kind = "number"
			}
			if containsWord(booleanFields, key) {
				kind = "boolean"
			}
			if strings.Contains(key, "password") || key == "bearer" || key == "schema_registry_bearer" {
				kind = "password"
			}
			// OPC UA object ids and attributes are identifiers, not integer controls.
			if id == "opcua" && (key == "object_id" || key == "attribute") {
				kind = "text"
			}
			hint := ""
			if strings.HasSuffix(key, "_file") || key == "next_config" {
				hint = "App-private relative path; import files using the Android document picker"
			}
			if key == "value" {
				hint = "JSON scalar or text; quote 64-bit integers to preserve precision"
				kind = "json"
			}
			if key == "word_order" {
				hint = "ABCD, CDAB, BADC or DCBA"
			}
			if key == "value_type" {
				hint = "Choose the protocol's exact value type (for example u16 / f32 or UInt16 / Double)"
			}
			p.Fields = append(p.Fields, Field{Key: key, Label: strings.ReplaceAll(key, "_", " "), Type: kind, Hint: hint})
		}
		protocols = append(protocols, p)
	}
	return map[string]any{"protocols": protocols, "utilities": []string{"config.get", "config.save", "config.validate", "config.import", "request.save", "request.delete", "profile.set", "options.set", "http.curl", "http.filter", "history.list", "history.get", "history.delete", "history.collections", "history.query", "history.preview", "history.execute", "crypto.convert", "crypto.transform", "modbus.encode", "modbus.rules", "modbus.import", "modbus.registers.import", "modbus.registers.export", "modbus.write-log", "file.read", "file.write", "files.list", "config.switch", "subscriptions.list", "subscriptions.stop", "subscriptions.stop-all", "opcua.connections", "opcua.connections.clear", "opcua.identity", "modbus.discovery.preview", "modbus.discovery.run", "modbus.controller.start", "modbus.snapshot.save", "modbus.snapshot.load", "modbus.snapshot.diff", "modbus.csv.diff", "modbus.interpret", "modbus.pause", "modbus.resume", "modbus.stats"}, "limits": map[string]int{"command_bytes": maxCommandBytes, "reply_bytes": maxReplyBytes, "event_bytes": maxEventBytes, "event_queue_bytes": maxQueueBytes, "event_count": maxQueueEvents}}
}
func actionDefaults(protocol, action string) map[string]any {
	p := map[string]any{}
	switch protocol {
	case "mqtt":
		p["topic"] = "iotools/demo"
		p["qos"] = 0
		if action == "subscribe" || action == "read-one" {
			p["limit"] = 100
		}
		if action == "publish" {
			p["payload"] = "hello"
			p["retain"] = false
		}
		if action == "preview-retained" {
			p["topic"] = "iotools/#"
			p["scan_duration_ms"] = 1000
			p["max_topics"] = 1000
		}
	case "kafka":
		if containsWord("consume produce topic-config offsets create-topic delete-topic alter-topic expand-partitions", action) {
			p["topic"] = "iotools-demo"
		}
		if action == "consume" {
			p["offset"] = "latest"
			p["limit"] = 100
		}
		if action == "produce" {
			p["key"] = "sensor"
			p["value"] = "hello"
		}
		if action == "create-topic" {
			p["partitions"] = 1
			p["replication_factor"] = 1
		}
		if containsWord("group lag delete-group", action) {
			p["group"] = "iotools-demo"
		}
		if action == "expand-partitions" {
			p["partitions"] = 2
		}
	case "modbus":
		p["unit"] = 1
		p["address"] = 0
		p["count"] = 1
		if action == "write-register" {
			p["value"] = 0
		}
		if action == "write-coil" {
			p["value"] = false
		}
		if action == "write-registers" || action == "read-write-registers" {
			p["values"] = []any{0}
		}
		if action == "write-coils" {
			p["values"] = []any{false}
		}
		if action == "write-typed" {
			p["value_type"] = "u16"
			p["value"] = "0"
			p["word_order"] = "ABCD"
		}
		if action == "read-write-registers" {
			p["read_address"] = 0
			p["read_count"] = 1
		}
		if strings.HasPrefix(action, "sweep-") || action == "search-holding" {
			p["end_address"] = 15
			p["sweep_cycles"] = 1
		}
		if action == "scan-units" {
			p["units"] = []any{1}
			p["scan_type"] = "read-holding"
		}
		if action == "search-holding" {
			p["match_value"] = 0
		}
		if action == "read-raw" {
			p["pdu_hex"] = "0300000001"
		}
	case "opcua":
		if action != "discover" {
			p["node_id"] = "i=85"
		}
		if action == "browse" || action == "references" {
			p["max_references"] = 1000
		}
		if action == "subscribe" {
			p["max_events"] = 1000
			p["interval_ms"] = 1000
		}
	}
	return p
}
