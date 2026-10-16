/**
 * The delivery panel's only external-link policy.
 *
 * A GitHub link is rendered from exactly two inputs: a repository slug that
 * already is GitHub's own `owner/repo` form, and a number or commit oid that is
 * encoded into the path. Nothing else produces an href, so a free-text display
 * name, a URL or a script-bearing string can never reach an anchor.
 */
import { githubRepositorySlug } from './presentation';

export type ProjectLink = { slug: string; displayName: string } | { mismatch: string } | { unusable: string };

export function projectLink(
  workspaceProject: { githubRepositoryId: string; displayName: string } | undefined,
  repositoryId: string,
): ProjectLink {
  if (workspaceProject === undefined) return { unusable: 'Workspace 投影不可用，无法确认目标仓库的登记显示名。' };
  if (workspaceProject.githubRepositoryId !== repositoryId) {
    return { mismatch: `Workspace 登记的项目仓是 ${workspaceProject.githubRepositoryId}，与本工作项的目标仓库 ${repositoryId} 不一致。` };
  }
  const slug = githubRepositorySlug(workspaceProject.displayName);
  if (slug === undefined) {
    return { unusable: `Workspace 登记的显示名“${workspaceProject.displayName}”不是 GitHub 的 owner/repo 形式，本页不据此拼接外链。` };
  }
  return { slug, displayName: workspaceProject.displayName };
}

export function slugOf(link: ProjectLink): string | undefined {
  return 'slug' in link ? link.slug : undefined;
}

export function projectLinkNote(project: ProjectLink): string | undefined {
  if ('mismatch' in project) return `未提供 GitHub 链接：${project.mismatch}`;
  if ('unusable' in project) return `未提供 GitHub 链接：${project.unusable}`;
  return undefined;
}
