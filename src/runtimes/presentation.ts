import type {
  Capability,
  GitRemoteStatus,
  MachineIdentityState,
  PushPermissionState,
  RemoteReadState,
  RuntimeFreshness,
  RuntimeStaleReason,
  RuntimeView,
} from '../api/contracts';
import type { BadgeTone } from '../ui/States';

export function parseCapabilities(text: string): Capability[] {
  const entries = text.split(/[\n,]/).map((line) => line.trim()).filter((line) => line !== '');
  return entries.map((entry) => {
    const match = /^([a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+)@([0-9]+)$/.exec(entry);
    if (!match) throw new Error(`能力格式必须是 id@version：${entry}`);
    const version = Number(match[2]);
    if (!Number.isInteger(version) || version < 1 || version > 65535) throw new Error(`能力版本超出范围：${entry}`);
    return { id: match[1] as string, version };
  });
}

export function formatCapabilitiesEditable(capabilities: Capability[]): string {
  return capabilities.map((capability) => `${capability.id}@${capability.version}`).join('\n');
}

export function formatCapabilities(capabilities: Capability[]): string {
  if (capabilities.length === 0) return '无';
  return capabilities.map((capability) => `${capability.id}@${capability.version}`).join('、');
}

export function formatTimestamp(value: string): string {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toISOString();
}

/**
 * `freshness` is a machine fact with three states and never collapses into a
 * single "healthy" answer: stale and unknown are never presented as usable.
 */
export function freshnessPresentation(freshness: RuntimeFreshness, staleReason?: RuntimeStaleReason): { tone: BadgeTone; label: string; detail: string } {
  if (freshness === 'fresh') {
    return { tone: 'ok', label: '新鲜', detail: '最近观测属于当前世代且在水位有效期内。' };
  }
  if (freshness === 'stale') {
    const detail = staleReason === 'superseded-epoch'
      ? '观测属于已被取代的运行世代；仅保留审计，不表示可用。'
      : '观测水位已过期；仅保留审计，不表示可用。';
    return { tone: 'warn', label: `陈旧（${staleReason ?? '未知原因'}）`, detail };
  }
  return { tone: 'unknown', label: '未知', detail: '该 Runtime 从未上报观测；未知不是可用。' };
}

export function machineIdentityPresentation(state: MachineIdentityState, registered: string[], observed?: string): { tone: BadgeTone; label: string } {
  if (state === 'matched') return { tone: 'ok', label: `已知机器身份一致（${observed ?? registered[0] ?? '—'}）` };
  if (state === 'drifted') return { tone: 'bad', label: `身份漂移：登记 ${registered.join('/') || '—'}，实际查询 ${observed ?? '—'}` };
  return { tone: 'unknown', label: '机器 GitHub 身份未知' };
}

export function remoteReadPresentation(state: RemoteReadState, transport?: string): { tone: BadgeTone; label: string } {
  if (state === 'readable') return { tone: 'ok', label: `只读探测成功${transport ? `（${transport}）` : ''}` };
  if (state === 'unreadable') return { tone: 'bad', label: '只读探测失败' };
  return { tone: 'unknown', label: '未探测' };
}

export function pushPermissionPresentation(state: PushPermissionState): { tone: BadgeTone; label: string } {
  if (state === 'granted') return { tone: 'ok', label: 'GitHub 报告具备 push 权限（不是写测试）' };
  if (state === 'denied') return { tone: 'bad', label: 'GitHub 报告无 push 权限' };
  return { tone: 'unknown', label: 'push 权限未知' };
}

export function runtimeHeading(runtime: RuntimeView): string {
  return `${runtime.runtimeId}（${runtime.providerId} ${runtime.providerVersion}）`;
}

export function gitRemoteSummary(status: GitRemoteStatus): string {
  if (status.probeOutcome === 'completed') return '只读探测已完成；本阶段没有任何真实写入观测。';
  return `探测未完成${status.probeReason ? `：${status.probeReason}` : ''}；未知状态不表示可用。`;
}
