// (c) 2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package cfg

import (
	"errors"
	"time"

	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/utils/logging"
	"github.com/go-redis/redis"
)

const appName = "ortelius"

var (
	ErrChainsConfigMustBeStringMap = errors.New("Chain config must a string map")
	ErrChainsConfigIDEmpty         = errors.New("Chain config ID is empty")
	ErrChainsConfigAliasEmpty      = errors.New("Chain config alias is empty")
	ErrChainsConfigVMEmpty         = errors.New("Chain config vm type is empty")
	ErrChainsConfigIDNotString     = errors.New("Chain config ID is not a string")
	ErrChainsConfigAliasNotString  = errors.New("Chain config alias is not a string")
	ErrChainsConfigVMNotString     = errors.New("Chain config vm type is not a string")
)

type Config struct {
	NetworkID uint32
	Logging   logging.Config
	Chains
	Stream
	Services
}

type Chain struct {
	ID     ids.ID
	Alias  string
	VMType string
}

type Chains map[ids.ID]Chain

type Services struct {
	API
	*DB
	Redis *redis.Options
}

type API struct {
	ListenAddr string
}

type DB struct {
	DSN    string
	Driver string
	TXDB   bool
}

type Stream struct {
	Kafka
	Filter
	Producer StreamProducer
	Consumer StreamConsumer
}

type Kafka struct {
	Brokers []string
}

type Filter struct {
	Min uint32
	Max uint32
}

type StreamProducer struct {
	IPCRoot string
}

type StreamConsumer struct {
	StartTime time.Time
	GroupName string
}

// NewFromFile creates a new *Config with the defaults replaced by the config  in
// the file at the given path
func NewFromFile(filePath string) (*Config, error) {
	v, err := newViperFromFile(filePath)
	if err != nil {
		return nil, err
	}

	// Get sub vipers for all objects with parents
	// chainsViper := newSubViper(v, keysChains)

	servicesViper := newSubViper(v, keysServices)
	servicesAPIViper := newSubViper(servicesViper, keysServicesAPI)
	servicesDBViper := newSubViper(servicesViper, keysServicesDB)
	servicesRedisViper := newSubViper(servicesViper, keysServicesRedis)

	streamViper := newSubViper(v, keysStream)
	streamKafkaViper := newSubViper(streamViper, keysStreamKafka)
	streamFilterViper := newSubViper(streamViper, keysStreamFilter)
	streamProducerViper := newSubViper(streamViper, keysStreamProducer)
	streamConsumerViper := newSubViper(streamViper, keysStreamConsumer)

	// Get logging config
	// We ignore the error because it's related to creating the default directory
	// but we are going to override it anyways
	logging, _ := logging.DefaultConfig()
	logging.Directory = v.GetString(keysLogDirectory)

	// Get chains config
	chains, err := newChainsConfig(v)
	if err != nil {
		return nil, err
	}

	// Put it all together
	return &Config{
		NetworkID: v.GetUint32(keysNetworkID),
		Logging:   logging,
		Chains:    chains,
		Services: Services{
			API: API{
				ListenAddr: servicesAPIViper.GetString(keysServicesAPIListenAddr),
			},
			DB: &DB{
				Driver: servicesDBViper.GetString(keysServicesDBDriver),
				DSN:    servicesDBViper.GetString(keysServicesDBDSN),
				TXDB:   servicesDBViper.GetBool(keysServicesDBTXDB),
			},
			Redis: &redis.Options{
				Addr:     servicesRedisViper.GetString(keysServicesRedisAddr),
				Password: servicesRedisViper.GetString(keysServicesRedisPassword),
				DB:       servicesRedisViper.GetInt(keysServicesRedisDB),
			},
		},
		Stream: Stream{
			Kafka: Kafka{
				Brokers: streamKafkaViper.GetStringSlice(keysStreamKafkaBrokers),
			},
			Filter: Filter{
				Min: streamFilterViper.GetUint32(keysStreamFilterMin),
				Max: streamFilterViper.GetUint32(keysStreamFilterMax),
			},
			Producer: StreamProducer{
				IPCRoot: streamProducerViper.GetString(keysStreamProducerIPCRoot),
			},
			Consumer: StreamConsumer{
				StartTime: streamConsumerViper.GetTime(keysStreamConsumerStartTime),
				GroupName: streamConsumerViper.GetString(keysStreamConsumerGroupName),
			},
		},
	}, nil
}
