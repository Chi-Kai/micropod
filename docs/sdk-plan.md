# Micropod Firecracker 客户端完全重构方案

## 背景

当前 micropod 项目使用自定义的 HTTP 客户端直接与 Firecracker REST API 通信，实现复杂且功能受限。作为新项目，我们有机会直接采用最佳实践，完全基于官方 `firecracker-go-sdk` 重构客户端实现。

参考 firectl 项目的成功实践，该项目使用官方 `firecracker-go-sdk v1.0.0` 实现了简洁、功能完整的 Firecracker 虚拟机管理。

## 目标

完全重写 micropod 的 Firecracker 集成层，直接基于官方 firecracker-go-sdk 构建，实现：
- 代码简化和架构优化
- 功能增强和扩展性提升  
- 企业级特性支持（Jailer、高级网络等）
- 更好的错误处理和日志记录

## 技术方案

### 1. 依赖更新

直接添加 firecracker-go-sdk 依赖到项目：

```go
// go.mod 更新
require (
    github.com/firecracker-microvm/firecracker-go-sdk v1.0.0
    github.com/sirupsen/logrus v1.9.3  // SDK 使用的日志库
)
```

### 2. 完全重构架构

#### 2.1 删除当前实现

完全删除现有的 `pkg/firecracker/client.go` (341行)，替换为基于 SDK 的简洁实现。

#### 2.2 新架构设计

```go
// 新实现：pkg/firecracker/client.go
type Client struct {
    machine      *firecracker.Machine
    config       firecracker.Config
    ctx          context.Context
    cancel       context.CancelFunc
    logger       *logrus.Entry
}

// 大幅简化的启动流程
client := firecracker.NewClient()
err := client.LaunchVM(LaunchConfig{...})
```

### 3. 完全重写实现

#### 3.1 新的 Client 实现 (pkg/firecracker/client.go)

```go
package firecracker

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    
    firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
    "github.com/firecracker-microvm/firecracker-go-sdk/client/models"
    "github.com/sirupsen/logrus"
    
    "micropod/pkg/network"
)

// 简化的配置结构
type LaunchConfig struct {
    KernelPath    string
    RootfsPath    string
    VCPUs         int64
    MemoryMB      int64
    BootArgs      string
    VirtioFS      *VirtioFSConfig
    Vsock         *VsockConfig
    Network       *network.Config
    SocketPath    string
    LogPath       string
}

type VirtioFSConfig struct {
    SharedDir  string
    MountTag   string
}

type VsockConfig struct {
    GuestCID uint32
    UDSPath  string
}

// 大幅简化的 Client 结构
type Client struct {
    machine *firecracker.Machine
    config  firecracker.Config
    ctx     context.Context
    cancel  context.CancelFunc
    logger  *logrus.Entry
}

func NewClient() *Client {
    ctx, cancel := context.WithCancel(context.Background())
    logger := logrus.WithField("component", "firecracker")
    
    return &Client{
        ctx:    ctx,
        cancel: cancel,
        logger: logger,
    }
}

func (c *Client) LaunchVM(config LaunchConfig) error {
    // 1. 构建 Firecracker 配置（参考 firectl 实现）
    fcConfig, err := c.buildConfig(config)
    if err != nil {
        return fmt.Errorf("failed to build config: %w", err)
    }
    c.config = fcConfig
    
    // 2. 设置机器选项
    opts := []firecracker.Opt{
        firecracker.WithLogger(c.logger),
    }
    
    // 3. 配置进程运行器（直接参考 firectl）
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

func (c *Client) buildConfig(config LaunchConfig) (firecracker.Config, error) {
    // 构建驱动器（参考 firectl 的 getBlockDevices）
    drives := []models.Drive{
        {
            DriveID:      firecracker.String("rootfs"),
            PathOnHost:   firecracker.String(config.RootfsPath),
            IsReadOnly:   firecracker.Bool(false),
            IsRootDevice: firecracker.Bool(true),
        },
    }
    
    // 构建网络接口（参考 firectl 的 getNetwork）
    var networkInterfaces []firecracker.NetworkInterface
    if config.Network != nil {
        networkInterfaces = []firecracker.NetworkInterface{
            {
                StaticConfiguration: &firecracker.StaticNetworkConfiguration{
                    MacAddress:  config.Network.GuestMAC,
                    HostDevName: config.Network.TapDevice,
                },
                AllowMMDS: false, // 根据需要开启 MMDS
            },
        }
    }
    
    // 构建 Vsock 设备（参考 firectl 的 parseVsocks）
    var vsockDevices []firecracker.VsockDevice
    if config.Vsock != nil {
        vsockDevices = []firecracker.VsockDevice{
            {
                Path: config.Vsock.UDSPath,
                CID:  config.Vsock.GuestCID,
            },
        }
    }
    
    // 构建机器配置（参考 firectl 的 MachineCfg）
    machineConfig := models.MachineConfiguration{
        VcpuCount:  firecracker.Int64(config.VCPUs),
        MemSizeMib: firecracker.Int64(config.MemoryMB),
        Smt:        firecracker.Bool(false),  // 禁用 SMT
    }
    
    return firecracker.Config{
        SocketPath:        config.SocketPath,
        KernelImagePath:   config.KernelPath,
        KernelArgs:        config.BootArgs,
        Drives:            drives,
        NetworkInterfaces: networkInterfaces,
        VsockDevices:      vsockDevices,
        MachineCfg:        machineConfig,
        LogLevel:          "Debug",
    }, nil
}

func (c *Client) Stop() error {
    if c.machine == nil {
        return nil
    }
    
    c.logger.Info("Stopping Firecracker VM...")
    
    // 优雅关闭，失败则强制停止（参考 firectl）
    if err := c.machine.Shutdown(c.ctx); err != nil {
        c.logger.WithError(err).Warn("Graceful shutdown failed, forcing stop")
        if stopErr := c.machine.StopVMM(); stopErr != nil {
            return fmt.Errorf("failed to stop VMM: %w", stopErr)
        }
    }
    
    c.cancel()
    return nil
}

func (c *Client) GetPID() int {
    if c.machine == nil {
        return 0
    }
    return c.machine.PID()
}

func (c *Client) IsRunning() bool {
    if c.machine == nil {
        return false
    }
    return c.machine.PID() > 0
}

// 等待 VM 结束（新增功能，SDK 提供）
func (c *Client) Wait() error {
    if c.machine == nil {
        return fmt.Errorf("machine not initialized")
    }
    return c.machine.Wait(c.ctx)
}

// 设置元数据（新增功能，SDK 提供）
func (c *Client) SetMetadata(metadata interface{}) error {
    if c.machine == nil {
        return fmt.Errorf("machine not initialized")
    }
    return c.machine.SetMetadata(c.ctx, metadata)
}
```

#### 3.2 Manager 层调用更新

更新 `pkg/manager/manager.go` 中的调用方式：

```go
// 原调用方式（复杂）
client := firecracker.NewClient(socketPath)
err := client.LaunchVM(kernelPath, agentRootfsPath, config.VCPUs, config.MemoryMB, 
    ipBootArg, virtioFS, vsock, netConfig, logFilePath)

// 新调用方式（简洁）
client := firecracker.NewClient()
err := client.LaunchVM(firecracker.LaunchConfig{
    KernelPath: kernelPath,
    RootfsPath: agentRootfsPath,
    VCPUs:      int64(config.VCPUs),
    MemoryMB:   int64(config.MemoryMB),
    BootArgs:   ipBootArg,
    VirtioFS:   virtioFS,
    Vsock:      vsock,
    Network:    netConfig,
    SocketPath: socketPath,
    LogPath:    logFilePath,
})
```

### 4. 实施步骤

#### 阶段 1: 依赖和基础设置
1. **更新依赖**: 添加 firecracker-go-sdk 到 go.mod
2. **删除旧实现**: 完全删除现有的 client.go (341行)
3. **创建新实现**: 实现基于 SDK 的新 client.go

#### 阶段 2: 核心重构
1. **实现新 Client**: 完成上述新的 Client 代码实现
2. **更新 Manager**: 修改 manager.go 中的调用方式
3. **更新配置结构**: 调整相关配置类型定义

#### 阶段 3: 测试和验证
1. **单元测试**: 为新实现编写全面的单元测试
2. **集成测试**: 验证与 manager、network、image 等模块的集成
3. **功能验证**: 确保所有现有功能正常工作

#### 阶段 4: 功能增强
1. **利用 SDK 新特性**: 添加元数据、等待、信号处理等功能
2. **优化错误处理**: 利用 SDK 的完善错误处理机制
3. **性能调优**: 基于 SDK 最佳实践优化配置

### 5. 重构收益

#### 5.1 代码简化
- **代码量减少**: 从 341 行减少到约 150 行
- **复杂度降低**: 不再需要手动管理 HTTP 客户端、进程启动等
- **维护性提升**: 跟随官方更新，自动获得 bug 修复和新特性

#### 5.2 功能增强
- **企业级特性**: 支持 Jailer、MMDS、高级网络配置
- **更好的日志**: 集成 logrus，提供结构化日志
- **信号处理**: 自动处理优雅关闭和强制停止
- **错误处理**: 更完善的错误类型和处理机制

### 6. 实施时间线

#### 第 1 周: 基础重构
- 更新 go.mod 添加 firecracker-go-sdk 依赖
- 删除旧的 client.go 实现
- 实现新的基于 SDK 的 Client

#### 第 2 周: 集成更新
- 更新 manager.go 调用方式
- 调整相关配置结构
- 修复编译和基础功能问题

#### 第 3 周: 测试和验证
- 编写新实现的单元测试
- 运行集成测试验证功能
- 性能对比和调优

#### 第 4 周: 功能增强
- 利用 SDK 新特性增强功能
- 优化错误处理和日志
- 文档更新

### 7. 验收标准

1. **功能完整性**: 
   - VM 启动、停止、状态查询正常
   - VirtioFS、Vsock、网络配置正常
   - Agent 通信功能正常

2. **代码质量**:
   - 代码行数减少 50%+ 
   - 单元测试覆盖率 ≥ 80%
   - 静态分析无严重问题

3. **性能指标**:
   - VM 启动时间无显著变化（±10%）
   - 内存使用无显著增加
   - CPU 使用稳定

4. **稳定性**:
   - 连续运行 24 小时无异常
   - 错误处理机制完善
   - 日志记录清晰可查

### 8. 附加收益

#### 8.1 未来扩展能力
- **Jailer 支持**: 可快速集成安全隔离功能
- **MMDS 支持**: 可添加实例元数据服务
- **多网卡支持**: 支持复杂网络拓扑
- **快照功能**: 利用 SDK 的快照 API

#### 8.2 开发体验改善
- **类型安全**: SDK 提供完整的类型定义
- **API 稳定**: 跟随官方 API 版本演进
- **社区支持**: 获得官方和社区支持
- **文档完善**: 官方文档和示例丰富

## 结论

通过完全重构为基于 firecracker-go-sdk 的实现，micropod 将获得：

- **代码简化**: 从 341 行减少到约 150 行，降低 55% 代码量
- **功能增强**: 获得企业级特性支持，为未来扩展奠定基础
- **维护性提升**: 跟随官方更新，减少维护工作量
- **开发效率**: 更好的类型安全和错误处理，提高开发体验

这一重构将为 micropod 成为生产级容器运行时打下坚实的技术基础，同时大幅降低长期维护成本。