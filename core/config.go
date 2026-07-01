package core

import (
	"encoding/json"
	"os"
)

type Config struct {
	ChainID uint64 `json:"chainId"`

	Network string `json:"network"`

	Version string `json:"version"`

	Port string `json:"port"`

	NodeID string `json:"nodeId"`
}

func LoadConfig(
	path string,
) (*Config, error) {

	data, err :=
		os.ReadFile(path)

	if err != nil {
		return nil, err
	}

	var cfg Config

	err =
		json.Unmarshal(
			data,
			&cfg,
		)

	if err != nil {
		return nil, err
	}

	return &cfg, nil
}