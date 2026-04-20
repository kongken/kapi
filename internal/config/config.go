package config

import "log/slog"

type ServiceConfig struct {
	Environment string `yaml:"environment"`
	HTTPPort    int    `yaml:"http_port"`
}

func (c *ServiceConfig) Print() {
	slog.Info("service config loaded",
		"environment", c.Environment,
		"http_port", c.HTTPPort,
	)
}
