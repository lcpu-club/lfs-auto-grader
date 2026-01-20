package adapters

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lcpu-club/lfs-auto-grader/pkg/aoiclient"
	"github.com/lcpu-club/lfs-auto-grader/pkg/judgerproto"
)

// PytestReportSummary pytest JSON 报告的 summary 部分
type PytestReportSummary struct {
	Passed    int `json:"passed"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	Total     int `json:"total"`
	Collected int `json:"collected"`
}

// PytestReport pytest --json-report 产出的 JSON 结构（简化版）
type PytestReport struct {
	Created     float64             `json:"created"`
	Duration    float64             `json:"duration"`
	ExitCode    int                 `json:"exitcode"`
	Root        string              `json:"root"`
	Environment map[string]any      `json:"environment"`
	Summary     PytestReportSummary `json:"summary"`
}

// LFS1Result 评测结果
type LFS1Result struct {
	Score   float64
	Status  string
	Message string
	Details *aoiclient.SolutionDetails
}

// ParsePytestReport 从文件解析 pytest JSON 报告
func ParsePytestReport(filepath string) (*PytestReport, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read report file: %w", err)
	}
	return ParsePytestReportFromBytes(data)
}

// ParsePytestReportFromBytes 从字节数组解析 pytest JSON 报告
func ParsePytestReportFromBytes(data []byte) (*PytestReport, error) {
	var report PytestReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("failed to parse report JSON: %w", err)
	}
	return &report, nil
}

// CalculateScore 根据 pytest 报告计算分数
// 分数 = (passed / total) * 100
func CalculateScore(report *PytestReport) *LFS1Result {
	summary := report.Summary
	total := summary.Total
	passed := summary.Passed

	// 计算分数
	var score float64
	if total > 0 {
		score = float64(passed) / float64(total) * 100
	} else {
		score = 0
	}

	// 确定状态
	var status string
	var message string

	if summary.Failed == 0 && passed == total {
		status = aoiclient.StatusAccepted
		message = fmt.Sprintf("全部通过 %d/%d 测试点", passed, total)
	} else if passed > 0 {
		status = aoiclient.StatusWrongAnswer
		message = fmt.Sprintf("通过 %d/%d 测试点，失败 %d 个", passed, total, summary.Failed)
	} else {
		status = aoiclient.StatusWrongAnswer
		message = fmt.Sprintf("未通过任何测试点 (0/%d)", total)
	}

	if summary.Skipped > 0 {
		message += fmt.Sprintf("，跳过 %d 个", summary.Skipped)
	}

	// 构建详情
	details := &aoiclient.SolutionDetails{
		Version: 1,
		Summary: message,
		Jobs: []*aoiclient.SolutionDetailsJob{
			{
				Name:       "pytest",
				Score:      score,
				ScoreScale: 100,
				Status:     status,
				Summary:    fmt.Sprintf("执行耗时 %.2f 秒", report.Duration),
				Tests: []*aoiclient.SolutionDetailsTest{
					{
						Name:       "通过",
						Score:      float64(passed),
						ScoreScale: float64(total),
						Status:     aoiclient.StatusAccepted,
						Summary:    fmt.Sprintf("%d 个测试通过", passed),
					},
					{
						Name:       "失败",
						Score:      0,
						ScoreScale: float64(summary.Failed),
						Status:     aoiclient.StatusWrongAnswer,
						Summary:    fmt.Sprintf("%d 个测试失败", summary.Failed),
					},
				},
			},
		},
	}

	return &LFS1Result{
		Score:   score,
		Status:  status,
		Message: message,
		Details: details,
	}
}

// ProcessAndPrint 处理报告并输出协议消息（供容器内使用）
func ProcessAndPrint(reportPath string) error {
	report, err := ParsePytestReport(reportPath)
	if err != nil {
		judgerproto.NewErrorMessage(err).Print()
		return err
	}

	result := CalculateScore(report)

	// 输出 Patch 消息
	judgerproto.NewPatchMessage(&judgerproto.PatchBody{
		Score:   result.Score,
		Status:  result.Status,
		Message: result.Message,
	}).Print()

	// 输出 Detail 消息
	judgerproto.NewDetailMessage((*judgerproto.DetailBody)(result.Details)).Print()

	// 输出 Complete 消息
	judgerproto.NewCompleteMessage().Print()

	return nil
}
