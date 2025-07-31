package config

import (
	"log"
	"os"
	"path/filepath"
)

type Config struct {
	ConfigDir string
}

func NewConfig() *Config {
	configDir := getConfigDir()
	return &Config{
		ConfigDir: configDir,
	}
}

func getConfigDir() string {
	if configDir := os.Getenv("MICROPOD_CONFIG_DIR"); configDir != "" {
		return configDir
	}
	
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/micropod"
	}
	
	return filepath.Join(homeDir, ".config", "micropod")
}

func (c *Config) GetKernelPath() string {
	kernelPath := filepath.Join(c.ConfigDir, "vmlinux", "vmlinux.elf")
	if _, err := os.Stat(kernelPath); os.IsNotExist(err) {
		log.Fatalf("Kernel file not found at %s. Please download a compatible kernel.", kernelPath)
	}
	return kernelPath
}

func (c *Config) GetStateFilePath() string {
	stateFilePath := filepath.Join(c.ConfigDir, "vms.json")
	if _, err := os.Stat(stateFilePath); os.IsNotExist(err) {
		// create a new state file
		if _, err := os.Create(stateFilePath); err != nil {
			log.Fatalf("Failed to create new state file: %v", err)
		}
	}
	return stateFilePath
}

func (c *Config) GetRootfsDir() string {
	rootfsDir := filepath.Join(c.ConfigDir, "rootfs")
	if _, err := os.Stat(rootfsDir); os.IsNotExist(err) {
		if err := os.MkdirAll(rootfsDir, 0755); err != nil {
			log.Fatalf("Failed to create rootfs directory: %v", err)
		}
	}
	return rootfsDir
}

func (c *Config) GetImageDir() string {
	imageDir := filepath.Join(c.ConfigDir, "images")
	if _, err := os.Stat(imageDir); os.IsNotExist(err) {
		if err := os.MkdirAll(imageDir, 0755); err != nil {
			log.Fatalf("Failed to create image directory: %v", err)
		}
	}
	return imageDir
}

func (c *Config) GetLogsDir() string {
	logsDir := filepath.Join(c.ConfigDir, "logs")
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		if err := os.MkdirAll(logsDir, 0755); err != nil {
			log.Fatalf("Failed to create logs directory: %v", err)
		}
	}
	return logsDir
}

func (c *Config) GetLogPath(vmID string) string {
	logPath := filepath.Join(c.GetLogsDir(), vmID+".log")
	
	// Create empty console log file if it doesn't exist (this is where VM output goes)
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		file, err := os.Create(logPath)
		if err != nil {
			log.Fatalf("Failed to create console log file: %v", err)
		}
		file.Close()
	}
	return logPath
}

func (c *Config) EnsureConfigDir() error {
	return os.MkdirAll(c.ConfigDir, 0755)
}