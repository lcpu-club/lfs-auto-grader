package adapters

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lcpu-club/lfs-auto-grader/pkg/aoiclient"
	"github.com/lcpu-club/lfs-auto-grader/pkg/judgerproto"
)

// PytestReportSummary pytest JSON 报告的 summary 部分
type PytestReportSummary struct {
	Passed    int `json:"passed"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	XFailed   int `json:"xfailed"`
	Total     int `json:"total"`
	Collected int `json:"collected"`
}

// PytestCrashInfo pytest 崩溃信息
type PytestCrashInfo struct {
	Path    string `json:"path"`
	Lineno  int    `json:"lineno"`
	Message string `json:"message"`
}

// PytestTestPhase pytest 测试阶段（setup/call/teardown）
type PytestTestPhase struct {
	Duration float64          `json:"duration"`
	Outcome  string           `json:"outcome"`
	Crash    *PytestCrashInfo `json:"crash,omitempty"`
	Longrepr string           `json:"longrepr,omitempty"`
}

// PytestTestCase pytest 单个测试用例
type PytestTestCase struct {
	NodeID   string           `json:"nodeid"`
	Lineno   int              `json:"lineno"`
	Outcome  string           `json:"outcome"`
	Keywords []string         `json:"keywords"`
	Setup    *PytestTestPhase `json:"setup,omitempty"`
	Call     *PytestTestPhase `json:"call,omitempty"`
	Teardown *PytestTestPhase `json:"teardown,omitempty"`
}

// PytestReport pytest --json-report 产出的 JSON 结构
type PytestReport struct {
	Created     float64             `json:"created"`
	Duration    float64             `json:"duration"`
	ExitCode    int                 `json:"exitcode"`
	Root        string              `json:"root"`
	Environment map[string]any      `json:"environment"`
	Summary     PytestReportSummary `json:"summary"`
	Tests       []PytestTestCase    `json:"tests"`
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

// extractTestName 从 nodeid 提取测试名称
// 例如: "tests/test_data.py::test_get_batch" -> "test_get_batch"
func extractTestName(nodeid string) string {
	// 按 "::" 分割，取最后一个部分
	parts := strings.Split(nodeid, "::")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return nodeid
}

// outcomeToStatus 将 pytest outcome 转换为 aoiclient status
func outcomeToStatus(outcome string) string {
	switch outcome {
	case "passed":
		return aoiclient.StatusAccepted
	case "failed":
		return aoiclient.StatusWrongAnswer
	case "skipped":
		return "Skipped"
	case "xfailed":
		return aoiclient.StatusAccepted // 预期失败，算通过
	case "xpassed":
		return aoiclient.StatusAccepted // 预期失败但通过了
	default:
		return aoiclient.StatusWrongAnswer
	}
}

// generateTestSummary 生成测试用例的摘要信息
func generateTestSummary(test *PytestTestCase) string {
	switch test.Outcome {
	case "passed":
		return "通过"
	case "xfailed":
		return "预期失败"
	case "xpassed":
		return "预期失败但通过"
	case "skipped":
		return "跳过"
	case "failed":
		// 尝试从 call.crash.message 获取错误信息
		if test.Call != nil && test.Call.Crash != nil && test.Call.Crash.Message != "" {
			return test.Call.Crash.Message
		}
		// 如果没有 crash 信息，尝试从 longrepr 获取（截取前 200 字符）
		if test.Call != nil && test.Call.Longrepr != "" {
			longrepr := test.Call.Longrepr
			if len(longrepr) > 200 {
				longrepr = longrepr[:200] + "..."
			}
			return longrepr
		}
		return "测试失败"
	default:
		return test.Outcome
	}
}

// CalculateScore 根据 pytest 报告计算分数
// 分数 = (passed / total) * 100
func CalculateScore(report *PytestReport) *LFS1Result {
	summary := report.Summary
	total := summary.Total
	// xfailed 算作通过
	passed := summary.Passed + summary.XFailed

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
	if summary.XFailed > 0 {
		message += fmt.Sprintf("，预期失败 %d 个", summary.XFailed)
	}

	// 为每个测试用例创建一个 Job
	jobs := make([]*aoiclient.SolutionDetailsJob, 0, len(report.Tests))
	for _, test := range report.Tests {
		testName := extractTestName(test.NodeID)
		testStatus := outcomeToStatus(test.Outcome)
		testSummary := generateTestSummary(&test)

		// 计算单个测试的分数
		var testScore float64
		if test.Outcome == "passed" || test.Outcome == "xfailed" || test.Outcome == "xpassed" {
			testScore = 100
		}

		jobs = append(jobs, &aoiclient.SolutionDetailsJob{
			Name:       testName,
			Score:      testScore,
			ScoreScale: 1,
			Status:     testStatus,
			Summary:    testSummary,
			Tests:      []*aoiclient.SolutionDetailsTest{},
		})
	}

	// 构建详情
	details := &aoiclient.SolutionDetails{
		Version: 1,
		Summary: message,
		Jobs:    jobs,
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
