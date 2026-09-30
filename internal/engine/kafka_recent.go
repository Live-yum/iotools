package engine

import (
	"context"
	"fmt"
	"github.com/Live-yum/iotools/internal/config"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"sort"
	"time"
)

func assignKafkaRecent(ctx context.Context, r config.Request, cl *kgo.Client, admin *kadm.Client, limit int) (map[int32]int64, error) {
	topic := r.String("topic", "")
	starts, err := admin.ListStartOffsets(ctx, topic)
	if err != nil {
		return nil, err
	}
	if err = starts.Error(); err != nil {
		return nil, err
	}
	ends, err := admin.ListEndOffsets(ctx, topic)
	if err != nil {
		return nil, err
	}
	if err = ends.Error(); err != nil {
		return nil, err
	}
	if start := r.String("start_time", ""); start != "" {
		stamp, err := kafkaStartTime(start, time.Now())
		if err != nil {
			return nil, err
		}
		starts, err = admin.ListOffsetsAfterMilli(ctx, stamp.UnixMilli(), topic)
		if err != nil {
			return nil, err
		}
		if err = starts.Error(); err != nil {
			return nil, err
		}
	}
	ids := []int32{}
	if v, exists := r.Params["consume_partitions"]; exists {
		ids, err = kafkaPartitions(v)
		if err != nil {
			return nil, err
		}
	} else if _, exists := r.Params["partition"]; exists {
		ids = []int32{int32(r.Int("partition", 0))}
	} else {
		for id := range ends[topic] {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > 1024 {
		return nil, fmt.Errorf("最近消息需要1..1024个有效分区")
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	per := int64((limit + len(ids) - 1) / len(ids))
	assigned := map[int32]kgo.Offset{}
	pending := map[int32]int64{}
	for _, id := range ids {
		end, ok := ends.Lookup(topic, id)
		if !ok {
			return nil, fmt.Errorf("分区%d不存在", id)
		}
		start, ok := starts.Lookup(topic, id)
		if !ok {
			return nil, fmt.Errorf("分区%d起点不存在", id)
		}
		if start.Offset < 0 || start.Offset >= end.Offset {
			continue
		}
		offset := end.Offset - per
		if offset < start.Offset {
			offset = start.Offset
		}
		assigned[id] = kgo.NewOffset().At(offset)
		pending[id] = end.Offset
	}
	if len(assigned) > 0 {
		cl.AddConsumePartitions(map[string]map[int32]kgo.Offset{topic: assigned})
	}
	return pending, nil
}
