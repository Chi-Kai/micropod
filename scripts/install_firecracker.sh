#!/bin/bash

# 定义 Firecracker 版本
VERSION="v1.12.1"

# 下载链接
DOWNLOAD_URL="https://github.com/firecracker-microvm/firecracker/releases/download/${VERSION}/firecracker-${VERSION}-x86_64.tgz"

# 定义下载和解压目录
TEMP_DIR="/tmp/firecracker_install"

# 检查是否已安装 curl
if ! command -v curl &> /dev/null
then
    echo "错误：'curl' 未安装。请先安装 'curl'。"
    exit 1
fi

echo "正在创建临时目录 ${TEMP_DIR}..."
mkdir -p "${TEMP_DIR}"
cd "${TEMP_DIR}"

echo "正在下载 Firecracker ${VERSION}..."
if curl -L "${DOWNLOAD_URL}" -o "firecracker.tgz"
then
    echo "下载成功。"
else
    echo "错误：下载失败。请检查 URL 或网络连接。"
    exit 1
fi

echo "正在解压文件..."
tar -xzf "firecracker.tgz"

echo "正在将 Firecracker 复制到 /usr/local/bin..."
# 确保 firecracker 可执行文件存在
if [ -f "release-${VERSION}-x86_64/firecracker-${VERSION}-x86_64" ]
then
    sudo cp "release-${VERSION}-x86_64/firecracker-${VERSION}-x86_64" /usr/local/bin/firecracker
    echo "Firecracker 安装成功！"
    echo "您现在可以通过运行 'firecracker --version' 来验证安装。"
else
    echo "错误：在解压后的目录中找不到 Firecracker 可执行文件。"
    exit 1
fi

# 清理临时文件
echo "正在清理临时文件..."
rm -rf "${TEMP_DIR}"

echo "清理完成。"