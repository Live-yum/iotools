package engine

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/Live-yum/iotools/internal/config"
)

var protocolParams = map[string]string{
	"http":   "headers body json bearer username password ca_file cert_file key_file",
	"mqtt":   "topic topics qos payload retain client_id username password ca_file cert_file key_file limit ignore_retained",
	"kafka":  "tls ca_file cert_file key_file sasl username password topic group groups partitions replication_factor configs key value offset limit filter subject version connector json headers body bearer key_format value_format key_subject value_subject key_version value_version schema_registry_url schema_registry_username schema_registry_password schema_registry_bearer schema_registry_ca_file schema_registry_cert_file schema_registry_key_file",
	"modbus": "address count unit value values samples interval_ms word_order baud data_bits parity stop_bits",
	"opcua":  "allow_insecure interval_ms max_events max_references auth auth_cert_file auth_key_file ca_file cert_file key_file method_id node_id node_ids object_id password security_mode security_policy server_cert_sha256 username value_type arguments value",
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
	ints := map[string][2]int{"address": {0, 65535}, "count": {1, 125}, "unit": {1, 247}, "qos": {0, 2}, "limit": {1, 100000}, "samples": {1, 100000}, "interval_ms": {10, 86400000}, "partitions": {1, 100000}, "replication_factor": {1, 32767}, "baud": {1, 4000000}, "data_bits": {5, 8}, "stop_bits": {1, 2}, "max_events": {1, 100000}, "max_references": {1, 100000}}
	for k, bounds := range ints {
		if v, ok := r.Params[k]; ok {
			n, e := exactInt(v)
			if e != nil || n < int64(bounds[0]) || n > int64(bounds[1]) {
				return fmt.Errorf("%s must be an integer in %d..%d", k, bounds[0], bounds[1])
			}
		}
	}
	for _, k := range []string{"tls", "retain", "ignore_retained", "allow_insecure"} {
		if v, ok := r.Params[k]; ok {
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s must be a YAML boolean", k)
			}
		}
	}
	if r.Protocol == "modbus" {
		if r.Mutates() {
			for _, k := range []string{"address", "unit"} {
				if _, ok := r.Params[k]; !ok {
					return fmt.Errorf("Modbus writes require an explicit %s", k)
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
