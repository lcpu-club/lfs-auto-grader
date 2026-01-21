# LFS Judge Docker Image
# 用于执行 pytest 评测并生成 JSON 报告

FROM ubuntu:24.04

# 安装基础工具（包括 bash 确保 shell 可用）
RUN apt-get update && apt-get install -y \
    bash \
    curl \
    ca-certificates \
    file \
    unzip \
    build-essential \
    cmake \
    && rm -rf /var/lib/apt/lists/*

# 安装 uv (Python 包管理器)
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
RUN PATH=/root/.local/bin:$PATH uv python install 3.13
ENV PATH="/root/.local/bin:$PATH"

# 创建工作目录
WORKDIR /home/judge

# 设置环境变量
ENV PYTHONUNBUFFERED=1
ENV PYTHONDONTWRITEBYTECODE=1
ENV UV_CACHE_DIR=/uv-cache

# 默认命令
CMD ["/bin/bash"]
