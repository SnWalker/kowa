import { useEffect, useRef, useState } from 'react';
import { ApiError, type ApiClient } from '../api/client';
import type { HumanDecision, HumanField, HumanTask } from '../api/contracts';
import { newRequestId } from '../api/session';
import { requireCsrf, useSession } from '../auth/session-context';
import { Button } from '../ui/button';
import { Field } from '../ui/Field';
import { Input } from '../ui/input';
import { Label } from '../ui/label';
import { RadioGroup, RadioGroupItem } from '../ui/radio-group';
import { Select } from '../ui/Select';
import { ErrorNotice, Notice } from '../ui/States';
import { Textarea } from '../ui/textarea';
import type { VersionConflict } from '../ui/version-conflict';
import { answerValues, humanFieldPresentation, unrenderableFields } from './presentation';

const DECISIONS: { value: HumanDecision; label: string }[] = [
  { value: 'approved', label: '批准（approved）' },
  { value: 'rejected', label: '驳回（rejected）' },
];

/**
 * The dynamic answer form of one versioned request.
 *
 * What it will not do, by construction:
 *
 * - submit anything the request did not declare (`answerValues` refuses a field
 *   this page cannot render, a choice outside the declared options, a missing
 *   required value, and a top-level decision that disagrees with the decision
 *   field);
 * - answer twice for one intent: the button is disabled while a submission is
 *   in flight and the same intent keeps the same request key, so a duplicate
 *   click or a retry after a transport failure reaches the server as one
 *   idempotent command;
 * - retry a dangerous decision by itself: a conflict re-reads the server, shows
 *   both versions and waits for an explicit confirmation against the version
 *   the server really holds.
 *
 * The draft (the chosen decision and every typed value) survives a refusal, so
 * nothing the member wrote is thrown away by a server answer.
 */
export function HumanTaskAnswer({
  client,
  workspaceId,
  task,
  onAccepted,
}: {
  client: ApiClient;
  workspaceId: string;
  task: HumanTask;
  onAccepted: (resolved: HumanTask) => void;
}) {
  const { state: sessionState } = useSession();
  const [decision, setDecision] = useState<HumanDecision | ''>('');
  const [values, setValues] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<unknown>(null);
  const [conflict, setConflict] = useState<VersionConflict | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const keys = useRef<{ fingerprint: string; key: string }>({ fingerprint: '', key: '' });
  const notices = useRef<HTMLDivElement>(null);

  // A refusal and a conflict are announced where the operator is: focus moves to
  // the outcome block so the next Tab continues from what went wrong, and the
  // answer itself is still on screen — nothing typed is discarded by an answer
  // the server refused.
  useEffect(() => {
    if (failure !== null || conflict !== null) notices.current?.focus();
  }, [failure, conflict]);

  const request = task.request;
  const decisionField = request.fields.find((field) => field.name === 'decision' && field.type === 'decision');
  const blocked = unrenderableFields(request.fields);
  // The owner freezes exactly three decision values; a request without its own
  // decision field is answered as `submitted`, never as an approval.
  const effectiveDecision: HumanDecision = decisionField ? (decision === '' ? 'submitted' : decision) : 'submitted';
  const candidate = answerValues({
    fields: request.fields,
    decision: effectiveDecision,
    values: decisionField ? { ...values, decision: decision === '' ? '' : decision } : values,
  });

  /**
   * The key names the intent, not the click: an identical payload re-submitted
   * after a transport failure or a refusal keeps its key and is idempotent on
   * the server, while a real change (a different decision, different values, a
   * new request version) is a new intent and takes a new key.
   */
  function requestKey(fingerprint: unknown): string {
    const serialized = JSON.stringify(fingerprint);
    if (keys.current.fingerprint !== serialized) {
      keys.current = { fingerprint: serialized, key: newRequestId('answer') };
    }
    return keys.current.key;
  }

  async function submit(expectedVersion: number) {
    if (!candidate.ok) {
      setFailure(new Error(candidate.reason));
      return;
    }
    setFailure(null);
    setStatus(null);
    setPending(true);
    try {
      const csrf = requireCsrf(sessionState);
      const resolved = await client.answerHumanTask(
        workspaceId,
        task.humanTaskId,
        {
          requestKey: requestKey({ expectedVersion, decision: effectiveDecision, values: candidate.values }),
          requestVersion: expectedVersion,
          object: request.object,
          decision: effectiveDecision,
          values: candidate.values,
        },
        csrf,
      );
      keys.current = { fingerprint: '', key: '' };
      setConflict(null);
      setStatus(
        `答复已由服务端接受（${resolved.humanTask.response?.humanResponseId ?? resolved.humanTask.humanTaskId}）。` +
          '服务端另行消费续接意图；是否推进流程由服务端与冻结定义裁决，本页不作断言。',
      );
      onAccepted(resolved.humanTask);
    } catch (error) {
      if (error instanceof ApiError && error.category === 'VERSION_CONFLICT') {
        // The request moved on underneath this submission. Re-read what the
        // server really holds and require an explicit answer against it; a
        // dangerous decision is never resubmitted automatically.
        try {
          const latest = await client.getHumanTask(workspaceId, task.humanTaskId);
          setConflict({ submittedVersion: expectedVersion, serverVersion: latest.humanTask.request.version });
          onAccepted(latest.humanTask);
        } catch (refreshError) {
          setFailure(refreshError);
        }
      } else {
        setFailure(error);
      }
    } finally {
      setPending(false);
    }
  }

  return (
    <>
      {/* The outcome block takes focus on a refusal; the notices inside keep
          their own alert/status roles. */}
      <div ref={notices} tabIndex={-1} className="outline-none">
        {conflict !== null && (
          <Notice
            tone="warning"
            title={`请求版本冲突：你提交时基于 v${conflict.submittedVersion}，服务端当前为 v${conflict.serverVersion}`}
          >
            <p>
              已重新读取该请求；你已选择的内容仍然保留。请核对服务端当前的所审版本后再显式提交（将使用新的请求键），
              本页不会自动重试这次答复。
            </p>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <Button type="button" onClick={() => void submit(conflict.serverVersion)} disabled={pending || !candidate.ok}>
                按服务端当前版本 v{conflict.serverVersion} 提交答复
              </Button>
              <Button type="button" variant="secondary" onClick={() => setConflict(null)}>取消</Button>
            </div>
          </Notice>
        )}
        {failure !== null && <ErrorNotice title="答复未被接受" error={failure} />}
        {status !== null && <Notice tone="info" title="已提交"><p>{status}</p></Notice>}
      </div>

      {request.fields.map((field) => (
        <FieldControl
          key={field.name}
          field={field}
          value={field.type === 'decision' ? decision : (values[field.name] ?? '')}
          disabled={pending || conflict !== null}
          onChange={(value) => {
            if (field.type === 'decision') setDecision(value as HumanDecision);
            else setValues((current) => ({ ...current, [field.name]: value }));
          }}
        />
      ))}

      {blocked.length > 0 && (
        <Notice tone="error" title="该请求包含本页无法渲染的字段">
          <p>
            {blocked.map((field) => field.name).join('、')}：本页没有该类型的控件（或是 choice 却没有登记可选值）。
            提交一个残缺的答复会与冻结请求不一致，因此这里不提供提交入口；请在支持这些字段的渠道答复。
          </p>
        </Notice>
      )}

      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button
          type="button"
          disabled={pending || conflict !== null || !candidate.ok}
          onClick={() => void submit(request.version)}
        >
          {pending ? '正在提交…' : `提交答复（基于请求 v${request.version}）`}
        </Button>
        <span className="text-xs text-muted-foreground">
          答复会连同请求版本与对象版本一起提交；服务端逐项校验身份、版本与字段，任何一项不符都会拒绝。
        </span>
      </div>
      {!candidate.ok && (
        <p className="mt-2 text-xs text-muted-foreground" role="status">{candidate.reason}</p>
      )}
    </>
  );
}

const fieldId = (name: string) => `answer-${name.replace(/[^A-Za-z0-9_-]/g, '-')}`;

/**
 * One declared field, drawn with the control its frozen type asks for. The
 * value always starts empty: a prefill would be the page answering on the
 * member's behalf, and the owner freezes that a prefilled value never counts as
 * an answer.
 */
function FieldControl({
  field,
  value,
  disabled,
  onChange,
}: {
  field: HumanField;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  const presentation = humanFieldPresentation(field);
  const id = fieldId(field.name);

  // A field this page cannot render gets no control at all: the section already
  // says why the request cannot be answered here, and drawing a substitute
  // input would invite a payload the owner never froze.
  if (unrenderableFields([field]).length > 0) {
    return (
      <div className="mt-3">
        <p className="flex flex-wrap items-baseline gap-1">
          <span className="text-sm font-semibold text-foreground">{presentation.label}</span>
          <span className="text-xs text-muted-foreground">（{presentation.required}）</span>
        </p>
        <p className="text-xs text-muted-foreground">{presentation.detail}</p>
      </div>
    );
  }

  if (field.type === 'decision') {
    const headingId = `${id}-label`;
    return (
      <div className="mt-3">
        <Label id={headingId} className="mb-1 flex-col items-start gap-0.5">
          <span className="flex flex-wrap items-baseline gap-1">
            <span className="text-sm font-semibold text-foreground">{presentation.label}</span>
            <span className="text-xs text-muted-foreground">（{presentation.required}）</span>
          </span>
          <span className="text-xs font-normal text-muted-foreground">{presentation.detail}</span>
        </Label>
        <RadioGroup aria-labelledby={headingId} value={value} onValueChange={onChange} disabled={disabled}>
          {DECISIONS.map((option) => (
            <div key={option.value} className="flex items-center gap-2">
              <RadioGroupItem id={`${id}-${option.value}`} value={option.value} aria-label={option.label} />
              <Label htmlFor={`${id}-${option.value}`} className="text-sm font-normal">{option.label}</Label>
            </div>
          ))}
        </RadioGroup>
      </div>
    );
  }

  if (field.type === 'choice') {
    return (
      <Field label={`${presentation.label}（${presentation.required}）`} className="mt-3 max-w-[36rem]">
        <Select
          aria-label={presentation.label}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
        >
          <option value="">（尚未选择）</option>
          {(field.options ?? []).map((option) => (
            <option key={option} value={option}>{option}</option>
          ))}
        </Select>
        <span className="text-xs text-muted-foreground">{presentation.detail}</span>
      </Field>
    );
  }

  if (field.type === 'comment') {
    return (
      <Field label={`${presentation.label}（${presentation.required}）`} className="mt-3">
        <Textarea
          id={id}
          aria-label={presentation.label}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
        />
        <span className="text-xs text-muted-foreground">{presentation.detail}</span>
      </Field>
    );
  }

  return (
    <Field label={`${presentation.label}（${presentation.required}）`} className="mt-3 max-w-[36rem]">
      <Input
        id={id}
        aria-label={presentation.label}
        autoComplete="off"
        spellCheck={false}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
      />
      <span className="text-xs text-muted-foreground">{presentation.detail}</span>
      {field.name === 'selection' && (
        <span className="text-xs text-muted-foreground">
          该字段是冻结索引中的路径（每行一条，顺序按冻结合同）。条目必须来自冻结索引，required 条目不可删除；
          运行详情页提供了按索引条目选择的形式，可避免手写路径带来的不一致。
        </span>
      )}
    </Field>
  );
}
