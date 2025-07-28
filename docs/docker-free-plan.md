
### **Micropod 原生 OCI 镜像处理技术方案**

#### 1\. 背景与目标

目前，`micropod` 的容器创建流程可能依赖于本地已有的、通过 Docker 或其他工具准备好的镜像文件系统。这种方式限制了 `micropod` 的独立部署和使用场景，使其更像一个“玩具”而非生产级工具。

**核心目标：**

1.  **独立性：** 让 `micropod` 具备独立从远端镜像仓库 (Registry) 拉取 (Pull) 镜像的能力，彻底移除对 Docker Daemon 的依赖。
2.  **标准化：** 遵循 OCI (Open Container Initiative) 镜像规范，确保 `micropod` 能处理所有符合社区标准的容器镜像。
3.  **高性能：** 设计高效的镜像分层存储和解压机制，最小化 I/O 和存储开销。

#### 2\. 核心设计

我们将围绕一个核心库，构建一套完整的镜像管理生命周期：`拉取 -> 存储 -> 解压`。

##### 2.1. 关键库选择

为了避免重复造轮子，同时保持代码库的简洁，我推荐使用 Google 开源的 Go 库：`google/go-containerregistry`。

**选择理由：**

  * **纯粹的 Go Library：** 它是一个纯粹的 Go 语言库，没有 C-Binding，没有复杂的依赖，不运行任何后台 Daemon，完美契合 `micropod` 的轻量级哲学。
  * **API 友好：** 提供了非常直观和简洁的 API 来与镜像仓库交互、读写 OCI 镜像格式。
  * **社区活跃：** 由 Google维护，社区活跃，是众多知名项目（如 aknode, Tekton, Skaffold）的选择，质量有保证。

##### 2.2. 整体架构

我们将新增一个 `ImageManager` 模块，它负责 `micropod` 所有的镜像操作。

```
+-------------------+      +-------------------------+      +----------------------+
|                   |      |                         |      |                      |
|  Micropod CLI/API |----->|      ImageManager       |----->|  Container Runtime   |
| (e.g., "run alpine") |      |                         |      | (e.g., "CreatePod")  |
+-------------------+      | +---------------------+ |      +----------------------+
                         | | 1. Pull & Store     | |
                         | +---------------------+ |
                         | | 2. Unpack (rootfs)  | |
                         | +---------------------+ |
                         +-----------+-------------+
                                     |
                                     | Uses "go-containerregistry"
                                     |
             +-----------------------+------------------------+
             |                                                |
+------------+-------------+                      +-----------+------------+
|   Remote Registry      |                      |    Local Storage     |
| (e.g., Docker Hub, GCR) |                      | (/var/lib/micropod/images) |
+------------------------+                      +------------------------+
```

##### 2.3. 镜像拉取与存储

我们将遵循 **OCI Image Layout** 规范在本地存储镜像。这是一个标准的、基于内容寻址的存储结构。

  * **存储路径：** 我们约定一个默认的存储根目录，例如 `	imageDir := filepath.Join(c.ConfigDir, "images")`。
  * **存储结构：**
      * `blobs/sha256/<hash>`: 存储镜像的所有 “blobs”，包括 **config** 和 **layers**。内容寻址确保了数据不重复。
      * `index.json`: 镜像的入口点，指向该镜像不同平台的 `manifest`。
      * `oci-layout`: 一个空文件，内容为 `{"imageLayoutVersion": "1.0.0"}`，用于标识这是一个 OCI Layout 目录。

**拉取流程：**

1.  用户执行 `micropod run alpine:latest`。
2.  `ImageManager` 检查本地是否存在 `alpine:latest` 镜像。
3.  如果不存在，`ImageManager` 使用 `go-containerregistry` 库：
    a. 解析镜像名 `alpine:latest`。
    b. 与远端 Registry 通信，获取 `manifest` 文件。
    c. 并发下载 `manifest` 中列出的所有 `layer` (blobs)。
    d. 将所有 `blobs` 存入本地的 `blobs/sha256/` 目录。
    e. 创建 `index.json` 指向该镜像的 `manifest`。

##### 2.4. Rootfs 构建 (镜像解压)

当容器需要被创建时，`ImageManager` 负责提供一个可用的根文件系统 (rootfs)。

**流程：**

1.  `Container Runtime` 请求 `ImageManager` 为 `alpine:latest` 准备 rootfs。
2.  `ImageManager` 读取本地存储的镜像 `manifest` 文件，获取所有 `layer` 的 `sha256` hash，并按顺序排列。
3.  为新容器创建一个 rootfs 目录 (e.g., `/home/chikai/.config/micropod/pods/<pod-id>/rootfs`)。
4.  **按顺序** 遍历所有 `layer`，将每一个 `layer` (通常是 `.tar.gz` 格式) 解压到 `rootfs` 目录中。后一个 layer 会覆盖前一个 layer 的同名文件，从而构建出最终的文件系统。
5.  为了效率，我们可以增加一个缓存机制，对于已经解压过的基础镜像，可以基于快照或写时复制 (Copy-on-Write) 文件系统 (如 OverlayFS) 来快速创建新的 rootfs，避免重复解压。

#### 3\. 核心 API 设计 (内部接口)

在 `micropod` 内部，我们可以定义一个简单的 `ImageService` 接口。

```go
package images

// ImageService defines the interface for managing container images.
type ImageService interface {
    // PullImage pulls an image from a remote registry and stores it locally.
    // refString is the image reference, e.g., "alpine:latest".
    PullImage(ctx context.Context, refString string) (Image, error)

    // GetImage retrieves image information from local storage.
    GetImage(ctx context.Context, refString string) (Image, error)

    // Unpack creates a root filesystem from a locally stored image.
    // It returns the path to the created rootfs.
    Unpack(ctx context.Context, refString string, destPath string) (string, error)

    // DeleteImage removes an image from local storage.
    DeleteImage(ctx context.Context, refString string) error
}

// Image represents a locally stored container image.
type Image interface {
    // Ref returns the original reference string.
    Ref() string
    // Digest returns the manifest digest (sha256:...).
    Digest() string
    // Layers returns the digests of all layers in order.
    Layers() []string
}
```

#### 4\. 实现步骤 (Roadmap)

1.  **Phase 1: 基础拉取与存储**

      * 引入 `google/go-containerregistry` 依赖。
      * 实现 `PullImage` 功能，能够成功从公共仓库（如 Docker Hub）拉取镜像并以 OCI Layout 格式存入本地。
      * 编写单元测试和集成测试。

2.  **Phase 2: 基础解压**

      * 实现 `Unpack` 功能，能够将一个本地镜像的所有 layers 按顺序解压到一个目标目录。
      * 集成到 `CreatePod` 流程中，替换掉旧的 rootfs 准备逻辑。

3.  **Phase 3: 优化与增强**

      * **认证支持：** 为 `PullImage` 增加私有仓库的认证功能 (username/password, token)。
      * **缓存优化：** 为 `Unpack` 引入 OverlayFS 支持，避免每次都完整解压。
      * **CLI 集成：** 添加新的 CLI 命令，如 `micropod images`, `micropod pull`, `micropod rmi`。

#### 5\. 核心代码示例 (Proof of Concept)

这是一个使用 `go-containerregistry` 拉取镜像并保存到本地 OCI Layout 的简单示例，它将成为我们 `ImageManager` 的核心逻辑。

```go
package main

import (
	"log"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/v1/layout"
)

// pullAndSaveImage demonstrates how to pull an image and save it as an OCI layout.
func pullAndSaveImage(imageRef, path string) {
	log.Printf("Pulling image %s...", imageRef)

	// 1. Pull the image
	img, err := crane.Pull(imageRef)
	if err != nil {
		log.Fatalf("failed to pull image %s: %v", imageRef, err)
	}

	log.Printf("Image pulled successfully. Digest: %s", img.Digest)

	// 2. Save the image to an OCI Layout on disk
	p, err := layout.FromPath(path)
	if err != nil {
        // If path does not exist, create it.
		p, err = layout.Write(path, nil)
		if err != nil {
			log.Fatalf("failed to create layout at path %s: %v", path, err)
		}
	}

	// Append the pulled image to the layout.
	if err := p.AppendImage(img); err != nil {
		log.Fatalf("failed to append image to layout: %v", err)
	}

	log.Printf("Image %s successfully saved to OCI layout at: %s", imageRef, path)
}

func main() {
	// Example usage:
	imageToPull := "docker.io/library/alpine:latest"
	storagePath := "/tmp/micropod-images"

	pullAndSaveImage(imageToPull, storagePath)
}
```

