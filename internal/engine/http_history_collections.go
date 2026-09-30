package engine

import (
	"context"
	"fmt"
	"strings"
)

func ListHTTPHistoryCollections(ctx context.Context, path string) ([]map[string]any, error) {
	return QueryHTTPHistory(ctx, path, "SELECT collection, count(*) AS requests, min(id) AS first_id, max(id) AS last_id FROM http_history GROUP BY collection ORDER BY collection LIMIT 1001")
}
func HTTPHistoryCollectionScript(action, source, target string) (string, error) {
	if source == "" || len(source) > 4096 || strings.ContainsRune(source, 0) {
		return "", fmt.Errorf("必须明确有效来源集合")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	switch action {
	case "delete":
		return "DELETE FROM http_history WHERE collection=" + quote(source) + "; SELECT changes() AS deleted", nil
	case "migrate":
		if target == "" || target == source || len(target) > 4096 || strings.ContainsRune(target, 0) {
			return "", fmt.Errorf("迁移目标集合必须非空且不同于来源")
		}
		return "UPDATE http_history SET collection=" + quote(target) + " WHERE collection=" + quote(source) + "; SELECT changes() AS migrated", nil
	default:
		return "", fmt.Errorf("未知集合操作")
	}
}
