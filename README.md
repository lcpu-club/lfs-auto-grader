# LFS Auto Grader

基于 Docker 的自动评测系统，与 AOI (Azukiiro) 平台集成。

## 快速开始

```bash
# 构建
go build -o grader ./cmd/manager

# 运行（需要先注册获取凭证）
export RUNNER_ID="your-id"
export RUNNER_KEY="your-key"
./grader
```

## 程序说明

| 程序 | 用途 |
|------|------|
| `cmd/manager` | 主评测服务，持续运行轮询任务 |
| `cmd/utility` | 工具（注册 Runner、调试） |
| `cmd/test` | 本地测试，不需要连接 AOI |

---

## 评测流程

```
AOI 后端 ──Poll──▶ Manager ──启动──▶ Docker 容器 ──输出──▶ Manager ──上报──▶ AOI
                      │                    │
                      │                    ├── 下载题目数据
                      │                    ├── 下载用户提交
                      │                    ├── 执行评测逻辑
                      │                    └── 输出评测结果
                      │
                      └── 解析输出，上报分数
```

---

## 输入：容器环境变量

Manager 启动容器时，会自动注入以下环境变量：

| 变量名 | 说明 | 示例 |
|--------|------|------|
| `SOLUTION_ID` | 提交 ID | `sol_abc123` |
| `TASK_ID` | 任务 ID | `task_xyz789` |
| `USER_ID` | 用户 ID | `user_001` |
| `SOLUTION_DATA_URL` | 用户提交文件下载地址 | `https://...` |
| `SOLUTION_DATA_HASH` | 提交文件哈希 | `sha256:...` |
| `PROBLEM_DATA_URL` | 题目数据下载地址 | `https://...` |
| `PROBLEM_DATA_HASH` | 题目数据哈希 | `sha256:...` |
| `JUDGE_VARIABLES` | 自定义变量 (JSON) | `{"testcases":10}` |

### 容器内获取数据

```bash
# 下载用户提交
curl -o /tmp/solution.zip "$SOLUTION_DATA_URL"

# 下载题目数据
curl -o /tmp/problem.zip "$PROBLEM_DATA_URL"
```

---

## 输出：评测结果协议

容器通过 **stdout** 输出 JSON 格式的消息，Manager 会解析并上报到 AOI。

### 消息格式

```json
{"t":"2026-01-20T10:00:00Z","a":"<action>","b":<body>}
```

| 字段 | 说明 |
|------|------|
| `t` | 时间戳 |
| `a` | 动作类型 |
| `b` | 消息体 |

### 动作类型

| Action | 说明 | Body |
|--------|------|------|
| `p` | **Patch** - 更新分数状态 | `SolutionInfo` |
| `d` | **Detail** - 保存详细结果 | `SolutionDetails` |
| `c` | **Complete** - 评测完成 | 无 |

### Patch 消息 (更新分数)

```json
{
  "t": "2026-01-20T10:00:00Z",
  "a": "p",
  "b": {
    "score": 85.5,
    "status": "Accepted",
    "message": "通过 17/20 测试点"
  }
}
```

**Body 字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `score` | float64 | 分数 (0-100) |
| `status` | string | 状态 |
| `message` | string | 简短消息 |

**可用状态：**
- `Accepted` - 通过
- `Wrong Answer` - 答案错误
- `Time Limit Exceeded` - 超时
- `Memory Limit Exceeded` - 内存超限
- `Runtime Error` - 运行错误
- `Compile Error` - 编译错误
- `Error` - 其他错误

### Detail 消息 (详细结果)

```json
{
  "t": "2026-01-20T10:00:00Z",
  "a": "d",
  "b": {
    "version": 1,
    "summary": "通过 17/20 测试点",
    "jobs": [
      {
        "name": "编译",
        "status": "Accepted",
        "score": 0,
        "scoreScale": 0,
        "summary": "编译成功",
        "tests": []
      },
      {
        "name": "评测",
        "status": "Wrong Answer",
        "score": 85,
        "scoreScale": 100,
        "summary": "",
        "tests": [
          {"name": "test1", "status": "Accepted", "score": 5, "scoreScale": 5, "summary": ""},
          {"name": "test2", "status": "Wrong Answer", "score": 0, "scoreScale": 5, "summary": "输出不匹配"}
        ]
      }
    ]
  }
}
```

### Complete 消息 (完成评测)

```json
{"t":"2026-01-20T10:00:00Z","a":"c"}
```

---

## 评测脚本示例

### Shell 脚本

```bash
#!/bin/sh
# /judge.sh

# 下载数据
curl -sL "$SOLUTION_DATA_URL" -o /tmp/solution.tar.gz
curl -sL "$PROBLEM_DATA_URL" -o /tmp/problem.tar.gz

# 解压
tar xzf /tmp/solution.tar.gz -C /work/solution
tar xzf /tmp/problem.tar.gz -C /work/problem

# 评测逻辑...
SCORE=100
STATUS="Accepted"

# 输出结果
echo '{"t":"'$(date -Iseconds)'","a":"p","b":{"score":'$SCORE',"status":"'$STATUS'","message":"All tests passed"}}'
echo '{"t":"'$(date -Iseconds)'","a":"c"}'
```

### Go 脚本 (使用 judgerproto)

```go
package main

import "github.com/lcpu-club/lfs-auto-grader/pkg/judgerproto"

func main() {
    // 评测逻辑...
    
    // 报告分数
    judgerproto.NewPatchMessage(&judgerproto.PatchBody{
        Score:   100,
        Status:  "Accepted",
        Message: "All tests passed",
    }).Print()
    
    // 完成
    judgerproto.NewCompleteMessage().Print()
}
```

---


## AOI 题目配置

在 AOI 后端设置题目的 `judge.config`：

```json
{
  "image": "your-judge-image:latest",
  "command": ["/judge.sh"],
  "timeout": 300,
  "memoryLimit": 512,
  "cpuLimit": 1.0,
  "env": {
    "LANG": "en_US.UTF-8"
  },
  "variables": {
    "testcases": 20
  }
}
```

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `image` | string | Docker 镜像 | 必填 |
| `command` | string[] | 执行命令 | 必填 |
| `timeout` | int64 | 超时(秒) | 300 |
| `memoryLimit` | int64 | 内存(MB) | 512 |
| `cpuLimit` | float64 | CPU 核心数 | 1.0 |
| `env` | object | 额外环境变量 | {} |
| `variables` | object | 自定义变量 | {} |

---

## 构建 Docker 镜像

```bash
docker build -t lfs-auto-grader:latest .
```

## License

MIT
