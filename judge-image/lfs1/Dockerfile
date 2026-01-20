# LFS Judge Docker Image
# 用于执行 pytest 评测并生成 JSON 报告

FROM python:3.12-slim

# 安装基础工具（包括 bash 确保 shell 可用）
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    curl \
    ca-certificates \
    file \
    unzip \
    && rm -rf /var/lib/apt/lists/*

# 安装 uv (Python 包管理器)
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:$PATH"

# 创建工作目录
WORKDIR /home/judge

# 设置环境变量
ENV PYTHONUNBUFFERED=1
ENV PYTHONDONTWRITEBYTECODE=1

# 创建评测脚本
COPY run.sh /run.sh
RUN chmod +x /run.sh

# 默认命令
CMD ["/bin/bash"]
