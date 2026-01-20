package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/lcpu-club/lfs-auto-grader/internal/adapters"
	"github.com/lcpu-club/lfs-auto-grader/internal/config"
	"github.com/lcpu-club/lfs-auto-grader/internal/executor"
	"github.com/lcpu-club/lfs-auto-grader/pkg/aoiclient"
	"github.com/lcpu-club/lfs-auto-grader/pkg/judgerproto"
)

const pollInterval = 250 * time.Millisecond

// MountConfig 挂载配置
type MountConfig struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

// RunningConfig 评测运行配置，对应 conf.json 中的 judge.config
type RunningConfig struct {
	Image       string            `json:"image"`       // Docker 镜像名
	PreCmd      []string          `json:"pre_cmd"`     // 预处理命令（评测前执行）
	DockerCmd   []string          `json:"docker_cmd"`  // Docker 容器内执行的命令
	PostCmd     []string          `json:"post_cmd"`    // 后处理命令（评测后执行）
	Timeout     int64             `json:"timeout"`     // 超时时间（秒）
	MemoryLimit int64             `json:"memoryLimit"` // 内存限制（MB）
	CPULimit    float64           `json:"cpuLimit"`    // CPU 限制（核心数）
	Env         map[string]string `json:"env"`         // 环境变量
	WorkDir     string            `json:"workDir"`     // 工作目录
	Mounts      []MountConfig     `json:"mounts"`      // 挂载配置
	Variables   map[string]any    `json:"variables"`   // 额外变量
}

type Manager struct {
	conf *config.ManagerConfig
	aoi  *aoiclient.Client
	exec *executor.DockerExecutor
}

func NewManager(conf *config.ManagerConfig) *Manager {
	return &Manager{conf: conf}
}

func (m *Manager) Init() error {
	exec, err := executor.NewDockerExecutor()
	if err != nil {
		return err
	}
	m.exec = exec

	aoi := aoiclient.New(*m.conf.Endpoint)
	if *m.conf.RunnerID != "" || *m.conf.RunnerKey != "" {
		aoi.Authenticate(*m.conf.RunnerID, *m.conf.RunnerKey)
	} else {
		return errors.New("runner ID and key must be provided")
	}
	m.aoi = aoi

	return nil
}

func (m *Manager) Start() error {
	for {
		time.Sleep(pollInterval)

		soln, err := m.aoi.Poll(context.TODO())
		if err != nil {
			log.Println("Failed to poll:", err)
			continue
		}

		if soln.SolutionId == "" || soln.TaskId == "" {
			continue
		}

		log.Println("Received solution", soln.SolutionId, "for task", soln.TaskId)

		// 打印完整的轮询返回信息
		if solnJSON, err := json.MarshalIndent(soln, "", "  "); err == nil {
			log.Printf("Full poll response:\n%s", string(solnJSON))
		}

		err = m.run(soln)
		if err != nil {
			log.Println("Failed to run solution:", err)
			m.failSoln(soln, "Failed to run solution: "+err.Error())
		}
	}
}

func (m *Manager) failSoln(soln *aoiclient.SolutionPoll, reason string) {
	s := m.aoi.Solution(soln.SolutionId, soln.TaskId)
	s.Patch(context.TODO(), &aoiclient.SolutionInfo{
		Score:   0,
		Status:  aoiclient.StatusError,
		Message: reason,
	})
	s.SaveDetails(context.TODO(), &aoiclient.SolutionDetails{Summary: reason})
	s.Complete(context.TODO())
}

func (m *Manager) run(soln *aoiclient.SolutionPoll) error {
	log.Printf("Starting evaluation for solution %s, task %s", soln.SolutionId, soln.TaskId)

	// 打印原始配置用于调试
	log.Printf("Raw judge config: %s", string(soln.ProblemConfig.Judge.Config))

	// 解析评测配置
	rc := new(RunningConfig)
	if err := json.Unmarshal(soln.ProblemConfig.Judge.Config, rc); err != nil {
		return fmt.Errorf("failed to parse judge config: %w", err)
	}

	// 打印解析后的配置用于调试
	log.Printf("Parsed config - Image: %s, DockerCmd: %v", rc.Image, rc.DockerCmd)

	aoi := m.aoi.Solution(soln.SolutionId, soln.TaskId)

	// 上报评测开始状态
	if err := aoi.Patch(context.TODO(), &aoiclient.SolutionInfo{
		Status:  "Running",
		Message: "评测开始",
	}); err != nil {
		log.Printf("Failed to patch running status: %v", err)
	}

	// 创建临时目录用于存放评测报告
	outputDir, err := os.MkdirTemp("", fmt.Sprintf("judge-output-%s-", soln.SolutionId))
	if err != nil {
		return fmt.Errorf("failed to create temp output dir: %w", err)
	}
	defer os.RemoveAll(outputDir) // 评测完成后清理临时目录

	log.Printf("Created temp output directory: %s", outputDir)

	execConfig, err := m.buildExecuteConfig(soln, rc, outputDir)
	if err != nil {
		return fmt.Errorf("failed to build execute config: %w", err)
	}

	// 设置超时上下文，额外增加 10 秒缓冲时间
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(execConfig.Timeout+10)*time.Second)
	defer cancel()

	// 执行评测容器
	result, err := m.exec.ExecuteWithLogs(ctx, execConfig, func(line string) error {
		log.Printf("[%s] %s", soln.SolutionId, line)
		m.processMessage(line, aoi)
		return nil
	})

	if err != nil {
		return fmt.Errorf("docker execution failed: %w", err)
	}

	// 处理特殊情况
	if result.TimedOut {
		log.Printf("Solution %s timed out", soln.SolutionId)
		aoi.Patch(context.TODO(), &aoiclient.SolutionInfo{
			Score:   0,
			Status:  aoiclient.StatusTimeLimitExceeded,
			Message: fmt.Sprintf("评测超时（限制 %d 秒）", execConfig.Timeout),
		})
		aoi.SaveDetails(context.TODO(), &aoiclient.SolutionDetails{
			Summary: fmt.Sprintf("评测超时，时间限制 %d 秒", execConfig.Timeout),
		})
		aoi.Complete(context.TODO())
		return nil
	}

	if result.OOM {
		log.Printf("Solution %s ran out of memory", soln.SolutionId)
		aoi.Patch(context.TODO(), &aoiclient.SolutionInfo{
			Score:   0,
			Status:  aoiclient.StatusMemoryLimitExceeded,
			Message: fmt.Sprintf("内存超限（限制 %d MB）", execConfig.MemoryLimit),
		})
		aoi.SaveDetails(context.TODO(), &aoiclient.SolutionDetails{
			Summary: fmt.Sprintf("内存超限，内存限制 %d MB", execConfig.MemoryLimit),
		})
		aoi.Complete(context.TODO())
		return nil
	}

	log.Printf("Solution %s finished with exit code %d", soln.SolutionId, result.ExitCode)

	// 从外部读取并解析评测报告
	reportProcessed := false
	adapter := soln.ProblemConfig.Judge.Adapter
	
	if adapter == "lfs1" {
		// 获取报告文件名（默认为 report.json）
		reportFileName := "report.json"
		if rc.Variables != nil {
			if reportName, ok := rc.Variables["report_name"].(string); ok && reportName != "" {
				reportFileName = reportName
			}
		}
		
		reportPath := filepath.Join(outputDir, reportFileName)
		log.Printf("Looking for report at: %s", reportPath)
		
		if _, err := os.Stat(reportPath); err == nil {
			// 报告文件存在，解析并上报
			log.Printf("Found report file, parsing with adapter: %s", adapter)
			
			report, err := adapters.ParsePytestReport(reportPath)
			if err != nil {
				log.Printf("Failed to parse report: %v", err)
				aoi.Patch(context.TODO(), &aoiclient.SolutionInfo{
					Score:   0,
					Status:  aoiclient.StatusInternalError,
					Message: fmt.Sprintf("解析评测报告失败: %v", err),
				})
			} else {
				// 使用 adapter 计算分数
				lfsResult := adapters.CalculateScore(report)
				
				// 上报结果给 AOI
				log.Printf("Reporting result: score=%.2f, status=%s", lfsResult.Score, lfsResult.Status)
				
				aoi.Patch(context.TODO(), &aoiclient.SolutionInfo{
					Score:   lfsResult.Score,
					Status:  lfsResult.Status,
					Message: lfsResult.Message,
				})
				
				if lfsResult.Details != nil {
					aoi.SaveDetails(context.TODO(), lfsResult.Details)
				}
				
				reportProcessed = true
			}
		} else {
			log.Printf("Report file not found at %s: %v", reportPath, err)
		}
	}

	// 如果没有处理报告且容器异常退出
	if !reportProcessed && result.ExitCode != 0 {
		log.Printf("Solution %s finished with non-zero exit code %d and no report", soln.SolutionId, result.ExitCode)
		aoi.Patch(context.TODO(), &aoiclient.SolutionInfo{
			Score:   0,
			Status:  aoiclient.StatusInternalError,
			Message: fmt.Sprintf("评测异常退出（退出码 %d），未找到评测报告", result.ExitCode),
		})
	}

	// 完成评测
	if err := aoi.Complete(context.TODO()); err != nil {
		log.Printf("Failed to complete solution: %v", err)
	}

	return nil
}

func (m *Manager) buildExecuteConfig(soln *aoiclient.SolutionPoll, rc *RunningConfig, outputDir string) (*executor.ExecuteConfig, error) {
	// 使用 docker_cmd 作为容器执行命令
	command := rc.DockerCmd
	if len(command) == 0 {
		return nil, fmt.Errorf("docker_cmd is required in judge config")
	}

	// 使用配置中的工作目录，如果未指定则使用默认值
	workDir := rc.WorkDir
	if workDir == "" {
		workDir = "/home/judge"
	}

	config := &executor.ExecuteConfig{
		Image:       rc.Image,
		Command:     command,
		Timeout:     rc.Timeout,
		MemoryLimit: rc.MemoryLimit,
		CPULimit:    rc.CPULimit,
		Env:         make(map[string]string),
		WorkDir:     workDir,
	}

	// 设置默认超时时间
	if config.Timeout == 0 {
		config.Timeout = 600 // 默认 10 分钟
	}
	// 设置默认内存限制
	if config.MemoryLimit == 0 {
		config.MemoryLimit = 2048 // 默认 2GB
	}

	// 复制用户自定义环境变量
	for k, v := range rc.Env {
		config.Env[k] = v
	}

	// 注入评测相关环境变量
	config.Env["SOLUTION_ID"] = soln.SolutionId
	config.Env["TASK_ID"] = soln.TaskId
	config.Env["USER_ID"] = soln.UserId
	config.Env["CONTEST_ID"] = soln.ContestId
	config.Env["SOLUTION_DATA_URL"] = soln.SolutionDataUrl
	config.Env["SOLUTION_DATA_HASH"] = soln.SolutionDataHash
	config.Env["PROBLEM_DATA_URL"] = soln.ProblemDataUrl
	config.Env["PROBLEM_DATA_HASH"] = soln.ProblemDataHash
	config.Env["PROBLEM_LABEL"] = soln.ProblemConfig.Label
	config.Env["JUDGE_ADAPTER"] = soln.ProblemConfig.Judge.Adapter

	// 注入额外变量
	if rc.Variables != nil {
		if varsJSON, err := json.Marshal(rc.Variables); err == nil {
			config.Env["JUDGE_VARIABLES"] = string(varsJSON)
		}
	}

	// 添加输出目录挂载（用于存放评测报告）
	// 容器内路径为 /output，容器需要将报告输出到此目录
	config.Mounts = append(config.Mounts, executor.Mount{
		Source:   outputDir,
		Target:   "/output",
		ReadOnly: false,
	})
	// 设置环境变量告知容器输出目录
	config.Env["OUTPUT_DIR"] = "/output"

	// 添加配置中指定的挂载
	for _, mount := range rc.Mounts {
		config.Mounts = append(config.Mounts, executor.Mount{
			Source:   mount.Source,
			Target:   mount.Target,
			ReadOnly: mount.ReadOnly,
		})
	}

	// 添加共享数据卷挂载
	if *m.conf.SharedVolumePath != "" {
		config.Mounts = append(config.Mounts, executor.Mount{
			Source:   *m.conf.SharedVolumePath,
			Target:   "/data",
			ReadOnly: true,
		})

		// 添加 uv 缓存目录挂载（提高评测速度）
		uvCachePath := *m.conf.SharedVolumePath + "/uv-cache"
		config.Mounts = append(config.Mounts, executor.Mount{
			Source:   uvCachePath,
			Target:   "/root/.cache/uv",
			ReadOnly: false,
		})
	}

	return config, nil
}

func (m *Manager) Close() error {
	if m.exec != nil {
		return m.exec.Close()
	}
	return nil
}

func (m *Manager) processMessage(msg string, aoi *aoiclient.SolutionClient) {
	parsed, err := judgerproto.MessageFromString(msg)
	if err != nil {
		// 非协议消息，忽略
		return
	}

	switch parsed.Action {
	case judgerproto.ActionGreet:
		// 容器发送的问候消息，表示容器已启动
		log.Printf("Received greet from container for solution %s", aoi.SolutionID())

	case judgerproto.ActionNoop:
		// 空操作，保持心跳
		// do nothing

	case judgerproto.ActionLog:
		// 日志消息
		var body judgerproto.LogBody
		if json.Unmarshal(parsed.Body, &body) == nil {
			log.Printf("[LOG %s] %s", aoi.SolutionID(), string(body))
		}

	case judgerproto.ActionError:
		// 错误消息
		var body judgerproto.ErrorBody
		if json.Unmarshal(parsed.Body, &body) == nil {
			log.Printf("[ERROR %s] %s", aoi.SolutionID(), string(body))
			// 上报错误状态
			aoi.Patch(context.TODO(), &aoiclient.SolutionInfo{
				Score:   0,
				Status:  aoiclient.StatusInternalError,
				Message: string(body),
			})
		}

	case judgerproto.ActionPatch:
		// 更新评测状态和分数
		var body judgerproto.PatchBody
		if json.Unmarshal(parsed.Body, &body) == nil {
			if err := aoi.Patch(context.TODO(), (*aoiclient.SolutionInfo)(&body)); err != nil {
				log.Printf("Failed to patch solution %s: %v", aoi.SolutionID(), err)
			} else {
				log.Printf("Patched solution %s: score=%.2f, status=%s", aoi.SolutionID(), body.Score, body.Status)
			}
		}

	case judgerproto.ActionDetail:
		// 保存评测详情
		var body judgerproto.DetailBody
		if json.Unmarshal(parsed.Body, &body) == nil {
			if err := aoi.SaveDetails(context.TODO(), (*aoiclient.SolutionDetails)(&body)); err != nil {
				log.Printf("Failed to save details for solution %s: %v", aoi.SolutionID(), err)
			} else {
				log.Printf("Saved details for solution %s", aoi.SolutionID())
			}
		}

	case judgerproto.ActionComplete:
		// 完成评测
		if err := aoi.Complete(context.TODO()); err != nil {
			log.Printf("Failed to complete solution %s: %v", aoi.SolutionID(), err)
		} else {
			log.Printf("Completed solution %s", aoi.SolutionID())
		}

	case judgerproto.ActionQuit:
		// 容器请求退出
		log.Printf("Received quit request from container for solution %s", aoi.SolutionID())
	}
}
