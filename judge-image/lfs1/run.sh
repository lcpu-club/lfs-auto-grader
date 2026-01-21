#!/bin/bash
set -e

echo "=== LFS Auto Grader ==="
echo "Solution ID: $SOLUTION_ID"
echo "Task ID: $TASK_ID"
echo "Output Directory: $OUTPUT_DIR"

# 下载用户提交
echo "Downloading solution..."
curl -sL "$SOLUTION_DATA_URL" -o /tmp/solution_file

# 检测文件类型并解压到工作目录
echo "Extracting solution..."
cd /home/judge

# 使用 file 命令检测文件类型
FILE_TYPE=$(file -b /tmp/solution_file)
echo "Detected file type: $FILE_TYPE"


if echo "$FILE_TYPE" | grep -q "Zip archive"; then
    echo "Extracting as zip..."
    
    if ! unzip -o /tmp/solution_file -d /home/judge; then
        echo "Failed to extract zip file"
        exit 1
    fi
    
    cd /home/judge
    
    ITEMS=(*)
    if [ ${#ITEMS[@]} -eq 1 ] && [ -d "${ITEMS[0]}" ]; then
        cd "${ITEMS[0]}"
        echo "Changed to: $(pwd)"
    else
        echo "Staying in: $(pwd)"
    fi
else
    echo "Unknown file type."
    exit 1
fi


# 运行 pytest 并生成 JSON 报告
echo "Running pytest..."
cp ./tests/adapters.py /tmp/adapters.py
rm -rf ./tests
cp -r /lfs-tests/ ./tests
rm -rf ./tests/adapters.py
cp /tmp/adapters.py ./tests/adapters.py
echo "3.13" >> .python-version
uv sync
uv add pytest-json-report
uv run pytest --json-report --json-report-file=report.json || true

# 输出报告内容 (用于调试)
echo "=== Test Report ==="
cat report.json 2>/dev/null || echo "No report.json found"

# 将报告复制到输出目录（供外部 adapter 解析）
if [ -n "$OUTPUT_DIR" ] && [ -d "$OUTPUT_DIR" ]; then
    echo "Copying report to output directory: $OUTPUT_DIR"
    cp report.json "$OUTPUT_DIR/" 2>/dev/null || echo "Failed to copy report.json"
fi

echo "=== Evaluation Complete ==="
