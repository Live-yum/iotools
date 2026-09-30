package engine

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/Live-yum/iotools/internal/config"
)

var protocolParams = map[string]string{
	"http":   "ignore_certificate_hosts follow_redirects max_redirects redirect_origins response_file max_response_bytes body_file body_stream max_upload_bytes query form_urlencoded form_multipart query_filter persist headers body json bearer username password ca_file cert_file key_file crypto request_crypto request_transforms response_transform",
	"mqtt":   "auto_reconnect reconnect_interval_ms scan_duration_ms max_topics confirm_topics confirm_token topic topics qos payload payload_encoding retain client_id username password ca_file cert_file key_file limit ignore_retained",
	"kafka":  "consume_partitions partition start_time key_filter key_prefix value_prefix confirm_subject tls ca_file cert_file key_file sasl username password topic group groups partitions replication_factor configs key value offset limit filter subject version connector json headers body bearer key_format value_format key_subject value_subject key_version value_version schema_registry_url schema_registry_username schema_registry_password schema_registry_bearer schema_registry_ca_file schema_registry_cert_file schema_registry_key_file",
	"modbus": "sweep_recover sweep_cycles scan_type stop_first read_address read_count next_config write_log_file write_log_previous columns keymap matrix_columns address count unit value values samples interval_ms word_order baud data_bits parity stop_bits pins labels rules units end_address match_value pdu_hex read_code object_id value_type",
	"opcua":  "allow_legacy_security browse_path auto_reconnect reconnect_interval_ms allow_insecure interval_ms max_events max_references auth auth_cert_file auth_key_file ca_file cert_file key_file method_id node_id node_ids attribute attributes direction reference_type include_subtypes object_id password security_mode security_policy server_cert_sha256 username value_type arguments value",
}

func validateParams(r config.Request) error {
	if strings.Contains(r.Endpoint, "://") {
		u, e := url.Parse(r.Endpoint)
		if e != nil {
			return fmt.Errorf("invalid endpoint URL")
		}
		if u.User != nil {
			return fmt.Errorf("endpoint must not embed credentials; use auth params with environment references")
		}
	}
	allowed := " " + protocolParams[r.Protocol] + " "
	for key := range r.Params {
		if !strings.Contains(allowed, " "+key+" ") {
			return fmt.Errorf("unknown %s parameter %q", r.Protocol, key)
		}
	}
	ints := map[string][2]int{"sweep_cycles": {1, 1000}, "reconnect_interval_ms": {100, 30000}, "scan_duration_ms": {100, 30000}, "max_topics": {1, 10000}, "partition": {0, 2147483647}, "matrix_columns": {1, 16}, "address": {0, 65535}, "count": {1, 125}, "unit": {1, 247}, "qos": {0, 2}, "limit": {1, 100000}, "samples": {1, 100000}, "interval_ms": {10, 86400000}, "partitions": {1, 100000}, "replication_factor": {1, 32767}, "baud": {1, 4000000}, "data_bits": {5, 8}, "stop_bits": {1, 2}, "max_events": {1, 100000}, "max_references": {1, 100000}}
	for k, bounds := range ints {
		if v, ok := r.Params[k]; ok {
			n, e := exactInt(v)
			if e != nil || n < int64(bounds[0]) || n > int64(bounds[1]) {
				return fmt.Errorf("%s must be an integer in %d..%d", k, bounds[0], bounds[1])
			}
		}
	}
	for _, k := range []string{"sweep_recover", "stop_first", "allow_legacy_security", "auto_reconnect", "persist", "tls", "retain", "ignore_retained", "allow_insecure", "include_subtypes"} {
		if v, ok := r.Params[k]; ok {
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s must be a YAML boolean", k)
			}
		}
	}
	if r.Protocol == "kafka" {
		if raw, ok := r.Params["consume_partitions"]; ok {
			if _, e := kafkaPartitions(raw); e != nil {
				return e
			}
			if _, exists := r.Params["partition"]; exists {
				return fmt.Errorf("partition 与 consume_partitions 不能同时提供")
			}
		}
	}
	if r.Protocol == "modbus" {
		if raw, exists := r.Params["scan_type"]; exists {
			value, ok := raw.(string)
			if !ok || (value != "read-holding" && value != "read-input" && value != "read-coils" && value != "read-discrete") {
				return fmt.Errorf("scan_type必须为四种明确读取空间之一")
			}
		}
		if raw, exists := r.Params["next_config"]; exists {
			path, ok := raw.(string)
			if !ok || strings.TrimSpace(path) == "" || len(path) > 4096 || strings.Contains(path, "://") || strings.Contains(path, "${") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, `\\`) || strings.Contains(path, "{{") || strings.Contains(path, "}}") || strings.IndexFunc(path, unicode.IsControl) >= 0 {
				return fmt.Errorf("next_config必须是1..4096字节的本机配置路径，不能是URL/模板/控制字符")
			}
			if i := strings.IndexByte(path, ':'); i >= 0 && !(i == 1 && len(path) > 2 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && (path[2] == '/' || path[2] == '\\')) {
				return fmt.Errorf("next_config不能为URI")
			}
		}
		if r.Mutates() {
			for _, k := range []string{"address", "unit"} {
				if _, ok := r.Params[k]; !ok {
					return fmt.Errorf("Modbus writes require an explicit %s", k)
				}
			}
		}
		if r.Action == "read-write-registers" {
			readAddress, errA := exactInt(r.Params["read_address"])
			readCount, errC := exactInt(r.Params["read_count"])
			if errA != nil || errC != nil || readAddress < 0 || readAddress > 65535 || readCount < 1 || readCount > 125 || readAddress+readCount > 65536 {
				return fmt.Errorf("FC23必须明确read_address/read_count，范围0..65535/1..125且不越界")
			}
			values, ok := r.Params["values"].([]any)
			if !ok || len(values) < 1 || len(values) > 121 {
				return fmt.Errorf("FC23 values必须为1..121个寄存器整数")
			}
			address, err := exactInt(r.Params["address"])
			if err != nil || address < 0 || address+int64(len(values)) > 65536 {
				return fmt.Errorf("FC23写范围越界")
			}
			for _, value := range values {
				n, err := exactInt(value)
				if err != nil || n < 0 || n > 65535 {
					return fmt.Errorf("FC23写值必须为0..65535整数")
				}
			}
		}
		if r.Action == "write-register" {
			v, ok := r.Params["value"]
			if !ok {
				return fmt.Errorf("write-register requires value")
			}
			n, e := exactInt(v)
			if e != nil || n < 0 || n > 65535 {
				return fmt.Errorf("value must be an integer in 0..65535")
			}
		}
		if r.Action == "write-coil" {
			if _, ok := r.Params["value"].(bool); !ok {
				return fmt.Errorf("write-coil requires an explicit YAML boolean value")
			}
		}
		if _, ok := r.Params["parity"]; ok {
			switch r.String("parity", "") {
			case "N", "E", "O":
			default:
				return fmt.Errorf("parity must be N, E or O")
			}
		}
	}
	return nil
}
func exactInt(v any) (int64, error) {
	switch n := v.(type) {
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	case uint64:
		if n <= math.MaxInt64 {
			return int64(n), nil
		}
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) && n == math.Trunc(n) && n >= math.MinInt64 && n < math.MaxInt64 {
			return int64(n), nil
		}
	case string:
		return strconv.ParseInt(n, 10, 64)
	}
	return 0, fmt.Errorf("not an exact integer")
}
