package firecracker

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/sirupsen/logrus"
)

// LaunchConfig 启动配置
type LaunchConfig struct {
	KernelPath   string
	RootfsPath   string
	VCPUs        int64
	MemoryMB     int64
	BootArgs     string
	SocketPath   string
	LogPath      string
}

// Client Firecracker 客户端
type Client struct {
	machine *firecracker.Machine
	config  firecracker.Config
	ctx     context.Context
	cancel  context.CancelFunc
	logger  *logrus.Entry
}

// NewClient 创建新的 Firecracker 客户端
func NewClient(socketPath string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	logger := logrus.WithField("component", "firecracker")

	return &Client{
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
		config: firecracker.Config{
			SocketPath: socketPath,
		},
	}
}

// Launch 启动虚拟机
func (c *Client) Launch(config LaunchConfig) error {
	// 1. 构建 Firecracker 配置
	fcConfig, err := c.buildConfig(config)
	if err != nil {
		return fmt.Errorf("failed to build config: %w", err)
	}
	c.config = fcConfig

	// 2. 设置机器选项
	opts := []firecracker.Opt{
		firecracker.WithLogger(c.logger),
	}

	// 3. 配置进程运行器
	firecrackerBinary, err := exec.LookPath("firecracker")
	if err != nil {
		return fmt.Errorf("firecracker binary not found: %w", err)
	}

	// 设置日志输出
	stdout := os.Stdout
	stderr := os.Stderr
	if config.LogPath != "" {
		logFile, err := os.OpenFile(config.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		stdout = logFile
		stderr = logFile
	}

	cmd := firecracker.VMCommandBuilder{}.
		WithBin(firecrackerBinary).
		WithSocketPath(fcConfig.SocketPath).
		WithStdin(os.Stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		Build(c.ctx)

	opts = append(opts, firecracker.WithProcessRunner(cmd))

	// 4. 创建并启动机器
	c.machine, err = firecracker.NewMachine(c.ctx, fcConfig, opts...)
	if err != nil {
		return fmt.Errorf("failed to create machine: %w", err)
	}

	if err := c.machine.Start(c.ctx); err != nil {
		return fmt.Errorf("failed to start machine: %w", err)
	}

	c.logger.Info("Firecracker VM started successfully")
	return nil
}

// buildConfig 构建 Firecracker 配置
func (c *Client) buildConfig(config LaunchConfig) (firecracker.Config, error) {
	// 构建驱动器
	drives := []models.Drive{
		{
			DriveID:      firecracker.String("rootfs"),
			PathOnHost:   firecracker.String(config.RootfsPath),
			IsReadOnly:   firecracker.Bool(false),
			IsRootDevice: firecracker.Bool(true),
		},
	}

	// 构建机器配置
	machineConfig := models.MachineConfiguration{
		VcpuCount:  firecracker.Int64(config.VCPUs),
		MemSizeMib: firecracker.Int64(config.MemoryMB),
		Smt:        firecracker.Bool(false), // 禁用 SMT
	}

	bootArgs := config.BootArgs
	if bootArgs == "" {
		bootArgs = "console=ttyS0 reboot=k panic=1 pci=off"
	}

	return firecracker.Config{
		SocketPath:      config.SocketPath,
		KernelImagePath: config.KernelPath,
		KernelArgs:      bootArgs,
		Drives:          drives,
		MachineCfg:      machineConfig,
		LogLevel:        "Debug",
	}, nil
}

// Stop 停止虚拟机
func (c *Client) Stop() error {
	if c.machine == nil {
		return nil
	}

	c.logger.Info("Stopping Firecracker VM...")

	// 优雅关闭，失败则强制停止
	if err := c.machine.Shutdown(c.ctx); err != nil {
		c.logger.WithError(err).Warn("Graceful shutdown failed, forcing stop")
		if stopErr := c.machine.StopVMM(); stopErr != nil {
			return fmt.Errorf("failed to stop VMM: %w", stopErr)
		}
	}

	c.cancel()
	return nil
}

// GetPID 获取进程 PID
func (c *Client) GetPID() int {
	if c.machine == nil {
		return 0
	}
	pid, err := c.machine.PID()
	if err != nil {
		return 0
	}
	return pid
}

// IsRunning 检查虚拟机是否运行中
func (c *Client) IsRunning() bool {
	if c.machine == nil {
		return false
	}
	pid, err := c.machine.PID()
	if err != nil {
		return false
	}
	return pid > 0
}

// Wait 等待 VM 结束
func (c *Client) Wait() error {
	if c.machine == nil {
		return fmt.Errorf("machine not initialized")
	}
	return c.machine.Wait(c.ctx)
}

// SetMetadata 设置元数据
func (c *Client) SetMetadata(metadata interface{}) error {
	if c.machine == nil {
		return fmt.Errorf("machine not initialized")
	}
	return c.machine.SetMetadata(c.ctx, metadata)
}