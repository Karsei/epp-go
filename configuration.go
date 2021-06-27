package epp

type Config struct {
        Database DatabaseConfig `yaml:"database"`
        Server   ServerConfig   `yaml:"server"`
}

type DatabaseConfig struct {
        Production  DatabaseEnv `yaml:"production"`
        Development DatabaseEnv `yaml:"development"`
}

type DatabaseEnv struct {
        Oracle DatabaseInfo `yaml:"oracle"`
}

type DatabaseInfo struct {
        Type     string `yaml:"type"`
        User     string `yaml:"user"`
        Password string `yaml:"password"`
        Host     string `yaml:"host"`
        Port     int    `yaml:"port"`
        Database string `yaml:"database"`
}

type ServerConfig struct {
        Port int `yaml:"port"`
}
