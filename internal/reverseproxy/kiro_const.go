package reverseproxy

// 本文件 1:1 迁移自 9router open-sse/config/kiroConstants.js（阶段 2：常量部分）。
//
// 镜像 CLIProxyAPIPlus 参考实现的 internal/translator/kiro/common/constants.go 与
// internal/translator/kiro/claude/kiro_claude_request.go，按 9router 所需范围裁剪：
//
//   - `-agentic` 模型后缀检测 + 分块写入系统提示
//   - 推理/思考触发检测（Anthropic-Beta 头、Claude `thinking`、OpenAI
//     `reasoning_effort`、AMP/Cursor 魔法标签）
//   - 开启 Kiro 推理的 `<thinking_mode>enabled</thinking_mode>` 系统提示注入
//
// Kiro 上游不公布 `-agentic` 模型 ID；它们是 9router 的虚构品。后缀在请求
// 离开本进程前剥离。

// KiroAgenticSuffix 9router 合成 agentic 变体后缀。
const KiroAgenticSuffix = "-agentic"

// KiroThinkingSuffix 9router 合成 thinking 变体后缀。
const KiroThinkingSuffix = "-thinking"

// KiroDefaultProfileARNs 公开的默认 CodeWhisperer profile ARN（us-east-1），按
// auth 方法分组。账号无法解析自身 profileArn 时使用。Builder ID 与社交
// （Google/GitHub）登录映射到不同的共享 profile。
var KiroDefaultProfileARNs = map[string]string{
	"builder-id": "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX",
	"social":     "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK",
}

// KiroDefaultProfileARN 向后兼容的单一默认值（Builder ID）。
// JS 原版为 KIRO_DEFAULT_PROFILE_ARNS["builder-id"]，Go 常量不能引用 map，故内联字面量。
const KiroDefaultProfileARN = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"

// ResolveDefaultProfileArn 为给定 auth 方法解析共享默认 profileArn。
func ResolveDefaultProfileArn(authMethod string) string {
	if authMethod == "google" || authMethod == "github" {
		return KiroDefaultProfileARNs["social"]
	}
	return KiroDefaultProfileARNs["builder-id"]
}

// KiroThinkingBudgetDefault 思考预算默认值。
const KiroThinkingBudgetDefault = 16000

// KiroAgenticSystemPrompt 当模型带 -agentic 后缀时注入的系统提示。
// 分块写入协议（强制）：违反将导致服务器超时与任务失败。
// 内容与 JS 原版 KIRO_AGENTIC_SYSTEM_PROMPT（.trim() 后）逐字一致。
const KiroAgenticSystemPrompt = `# CRITICAL: CHUNKED WRITE PROTOCOL (MANDATORY)

You MUST follow these rules for ALL file operations. Violation causes server timeouts and task failure.

## ABSOLUTE LIMITS
- **MAXIMUM 350 LINES** per single write/edit operation - NO EXCEPTIONS
- **RECOMMENDED 300 LINES** or less for optimal performance
- **NEVER** write entire files in one operation if >300 lines

## MANDATORY CHUNKED WRITE STRATEGY

### For NEW FILES (>300 lines total):
1. FIRST: Write initial chunk (first 250-300 lines) using write_to_file/fsWrite
2. THEN: Append remaining content in 250-300 line chunks using file append operations
3. REPEAT: Continue appending until complete

### For EDITING EXISTING FILES:
1. Use surgical edits (apply_diff/targeted edits) - change ONLY what's needed
2. NEVER rewrite entire files - use incremental modifications
3. Split large refactors into multiple small, focused edits

### For LARGE CODE GENERATION:
1. Generate in logical sections (imports, types, functions separately)
2. Write each section as a separate operation
3. Use append operations for subsequent sections

## EXAMPLES OF CORRECT BEHAVIOR

CORRECT: Writing a 600-line file
- Operation 1: Write lines 1-300 (initial file creation)
- Operation 2: Append lines 301-600

CORRECT: Editing multiple functions
- Operation 1: Edit function A
- Operation 2: Edit function B
- Operation 3: Edit function C

WRONG: Writing 500 lines in single operation -> TIMEOUT
WRONG: Rewriting entire file to change 5 lines -> TIMEOUT
WRONG: Generating massive code blocks without chunking -> TIMEOUT

## WHY THIS MATTERS
- Server has 2-3 minute timeout for operations
- Large writes exceed timeout and FAIL completely
- Chunked writes are FASTER and more RELIABLE
- Failed writes waste time and require retry

REMEMBER: When in doubt, write LESS per operation. Multiple small operations > one large operation.`
