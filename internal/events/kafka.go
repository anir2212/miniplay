package events

import (
	"context"
	"errors"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
)

type Kafka struct{ Client *kgo.Client }

func NewKafka(brokerList string) (*Kafka, error) {
	if strings.TrimSpace(brokerList) == "" {
		return nil, errors.New("KAFKA_BROKERS is required")
	}
	client, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(brokerList, ",")...))
	if err != nil {
		return nil, err
	}
	return &Kafka{Client: client}, nil
}

func (k *Kafka) Publish(ctx context.Context, topic, key string, payload []byte) error {
	return k.Client.ProduceSync(ctx, &kgo.Record{Topic: topic, Key: []byte(key), Value: payload}).FirstErr()
}
func (k *Kafka) Close() { k.Client.Close() }
