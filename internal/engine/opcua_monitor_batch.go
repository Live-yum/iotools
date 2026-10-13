package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopcua/opcua/ua"
)

type opcuaMonitor interface {
	Monitor(context.Context, ua.TimestampsToReturn, ...*ua.MonitoredItemCreateRequest) (*ua.CreateMonitoredItemsResponse, error)
}

// Keep one subscription/session, splitting only CreateMonitoredItems requests.
// Handles remain globally indexed so notifications map to the original nodes.
func opcuaMonitorBatches(ctx context.Context, sub opcuaMonitor, items []*ua.MonitoredItemCreateRequest) error {
	batchSize := 100
	for offset := 0; offset < len(items); {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(offset+batchSize, len(items))
		result, err := sub.Monitor(ctx, ua.TimestampsToReturnBoth, items[offset:end]...)
		if errors.Is(err, ua.StatusBadTooManyOperations) && batchSize > 1 {
			batchSize = max(1, batchSize/2)
			continue
		}
		if err != nil {
			return err
		}
		if result == nil || len(result.Results) != end-offset {
			return fmt.Errorf("incomplete monitor response")
		}
		for i, item := range result.Results {
			if item == nil {
				return fmt.Errorf("empty monitor result for %s", items[offset+i].ItemToMonitor.NodeID)
			}
			if item.StatusCode != ua.StatusOK {
				return fmt.Errorf("server rejected monitored item %s: %w", items[offset+i].ItemToMonitor.NodeID, item.StatusCode)
			}
		}
		offset = end
	}
	return nil
}
