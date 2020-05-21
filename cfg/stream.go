// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package cfg

import (
	"time"

	"github.com/spf13/viper"
)

const (
	configKeysIPCRoot = "ipcRoot"

	configKeysFilter    = "filter"
	configKeysFilterMin = "min"
	configKeysFilterMax = "max"

	configKeysKafka          = "kafka"
	configKeysKafkaBrokers   = "brokers"
	configKeysKafkaGroupName = "groupName"
)

type StreamProducer struct {
	Common
	Kafka
	IPCRoot string
}

type StreamConsumer struct {
	Common
	Kafka
	StartTime time.Time
	GroupName string
}

type Kafka struct {
	Filter
	Brokers []string
}

type Filter struct {
	Min uint32
	Max uint32
}

func NewStreamProducer(file string) (StreamProducer, error) {
	v, common, err := getConfigViper(file)
	if err != nil {
		return StreamProducer{}, err
	}

	return StreamProducer{
		Common: common,
		Kafka:  getKafkaConf(v),
	}, nil
}

func NewStreamConsumer(file string) (StreamConsumer, error) {
	v, common, err := getConfigViper(file)
	if err != nil {
		return StreamConsumer{}, err
	}

	return StreamConsumer{
		Common: common,
		Kafka:  getKafkaConf(v),

		// We should have exactly 1 of the following
		// StartTime:
		GroupName: v.GetString(configKeysKafkaGroupName),
	}, nil
}

func getKafkaConf(v *viper.Viper) Kafka {
	if v == nil {
		return Kafka{}
	}
	return Kafka{
		Filter: Filter{
			Min: v.GetUint32(configKeysFilterMin),
			Max: v.GetUint32(configKeysFilterMax),
		},

		IPCRoot: v.GetString(configKeysIPCRoot),
		Brokers: v.GetStringSlice(configKeysKafkaBrokers),
	}
}
