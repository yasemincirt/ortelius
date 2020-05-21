// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package stream

import (
	"context"

	"github.com/ava-labs/gecko/ids"
	"github.com/segmentio/kafka-go"

	"github.com/ava-labs/ortelius/cfg"
	"github.com/ava-labs/ortelius/services"
	"github.com/ava-labs/ortelius/stream/record"
)

type serviceConsumerFactory func(cfg.ServiceConfig, uint32, cfg.ChainConfig) (services.Consumer, error)

// consumer takes events from Kafka and sends them to a service consumer
type consumer struct {
	reader   *kafka.Reader
	consumer services.Consumer
}

// NewConsumerFactory returns a processorFactory for the given service consumer
func NewConsumerFactory(factory serviceConsumerFactory) ProcessorFactory {
	return func(conf cfg.ClientConfig, networkID uint32, chainConfig cfg.ChainConfig) (Processor, error) {
		var (
			err error
			c   = &consumer{}
		)

		// Create consumer backend
		c.consumer, err = factory(conf.ServiceConfig, networkID, chainConfig)
		if err != nil {
			return nil, err
		}

		// Bootstrap our index
		if err = c.consumer.Bootstrap(); err != nil {
			return nil, err
		}

		// Create reader for the topic
		c.reader = kafka.NewReader(kafka.ReaderConfig{
			Topic:    chainConfig.ID.String(),
			Brokers:  conf.KafkaConfig.Brokers,
			GroupID:  conf.KafkaConfig.GroupName,
			MaxBytes: 10e6,
		})

		return c, nil
	}
}

// Close closes the consumer
func (c *consumer) Close() error {
	return c.reader.Close()
}

// ProcessNextMessage waits for a new Message and adds it to the services
func (c *consumer) ProcessNextMessage(ctx context.Context) (*Message, error) {
	msg, err := getNextMessage(ctx, c.reader)
	if err != nil {
		return nil, err
	}
	return msg, c.consumer.Consume(msg)
}

// getNextMessage gets the next Message from the Kafka Indexer
func getNextMessage(ctx context.Context, r *kafka.Reader) (*Message, error) {
	// Get raw Message from Kafka
	msg, err := r.ReadMessage(ctx)
	if err != nil {
		return nil, err
	}

	// Extract chainID from topic
	chainID, err := ids.FromString(msg.Topic)
	if err != nil {
		return nil, err
	}

	// Extract Message ID from key
	id, err := ids.ToID(msg.Key)
	if err != nil {
		return nil, err
	}

	// Extract tx body from value
	body, err := record.Unmarshal(msg.Value)
	if err != nil {
		return nil, err
	}

	return &Message{
		id:        id,
		chainID:   chainID,
		body:      body,
		timestamp: msg.Time.UTC().Unix(),
	}, nil
}
