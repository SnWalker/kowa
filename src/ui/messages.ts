import { ApiError } from '../api/client';
import type { ApiErrorCategory } from '../api/contracts';
import { ContractViolationError } from '../api/decode';

/**
 * Presentation of the frozen failure categories. These sentences describe the
 * category; they never turn a refusal into success, never present a denial as
 * "not found" (or the reverse), and never claim a permission the server did
 * not confirm.
 */
const CATEGORY_TEXT: Record<ApiErrorCategory, string> = {
  UNAUTHORIZED: '会话已失效或未被服务端接受；需要重新登录。',
  FORBIDDEN: '服务端拒绝：当前身份没有该对象或 Workspace 的权限。',
  VERSION_CONFLICT: '对象版本已变化，请基于服务端最新版本确认后重新提交。',
  RESOURCE_UNAVAILABLE: '外部依赖或配置当前不可用（例如仓库不可物化、GitHub 安装不可见）；这不表示对象不存在，也不构成权限证明。',
  EXTERNAL_UNKNOWN: '外部事实尚未核对；不把未确认的观察当作通过或失败。',
  POLICY_BLOCKED: '违反策略或被服务端门禁阻断（例如第二个可写项目仓、必需知识条目被删除、运行非活跃）。',
  VALIDATION_ERROR: '服务端未接受该请求，请检查输入后重试。',
  INTERNAL_ERROR: '服务端内部错误，请稍后重试。',
};

export function describeError(error: unknown): string {
  if (error instanceof ApiError) {
    return error.category
      ? `${CATEGORY_TEXT[error.category]}（${error.category}）`
      : `请求失败，且响应不符合冻结错误信封；按未知错误处理，不重试写操作。（HTTP ${error.status}）`;
  }
  if (error instanceof ContractViolationError) {
    return `服务端响应与冻结合同不一致，已按契约违规拒绝使用该数据：${error.message}`;
  }
  if (error instanceof Error) return error.message;
  return '发生未知错误。';
}
