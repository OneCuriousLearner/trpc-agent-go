# gvm cd 钩子与 Claude Code/tclaude shell 快照守卫

> 2026-07-08 整理。本文记录 `_encode/_decode: command not found` 刷屏问题的根因、守卫方案与验证。CLAUDE.md 仅保留浓缩版 + 指向本文的索引。

## 问题现象

在 Claude Code/tclaude 的 bash 会话里,每条带 `cd ...` 的命令都打印若干行:

```
/root/.tclaude/shell-snapshots/snapshot-bash-<ts>-<rand>.sh: line NNNN: _encode: command not found
/root/.tclaude/shell-snapshots/snapshot-bash-<ts>-<rand>.sh: line NNNN: _decode: command not found
```

不影响命令执行(退出码正常),但严重污染输出,导致每条命令都得套 `grep -vE "_encode|_decode"` 过滤。

## 根因

Claude Code/tclaude 生成 bash 快照时,用 `grep -vE '^_[^_]'` 过滤函数——本意是丢掉补全函数(单下划线前缀),但**误伤了 gvm 的 `_encode`/`_decode`**(它们是单下划线前缀的普通函数,不是补全函数)。问题在于:过滤丢了 `_encode`/`_decode` 的**定义**,却**保留了调用它们的 gvm `cd` 钩子**。于是每条 `cd ...` 命令都触发 `cd` 钩子 → 钩子调用已不存在的 `_encode`/`_decode` → `command not found`。

## 守卫方案(`~/.bashrc`)

`~/.bashrc` 末尾加一段 `CLAUDECODE` 守卫(仅对 `CLAUDECODE=1` 的 shell 生效):

```bash
if [[ -n "$CLAUDECODE" ]]; then
	unset -f cd 2>/dev/null
fi
```

在快照 shell 里卸掉 gvm 的 `cd` 钩子、恢复 builtin `cd`,从根上消除触发点(不再有人调用 `_encode`/`_decode`,报错自然消失)。正常交互式终端(无 `CLAUDECODE`)不受影响,gvm cd 钩子照常工作。

这是**根治触发点**,不是过滤日志——比 `grep -vE` 过滤更彻底(过滤只是遮掉报错,守卫让报错根本不发生)。

## 生效条件

tclaude 每个会话只创建一次快照并复用,改完 `.bashrc` 后**必须重启会话**才会用上新快照。验证方式:
- 执行带 `cd` 的命令(如 `cd ... && wc -l *.ts`)不再出现 `_encode/_decode` 报错即生效;
- `type cd` 输出 `cd is a shell builtin`(说明 cd 钩子已卸);
- `/root/.tclaude/shell-snapshots/` 里新快照不含 `cd` 函数(函数数比旧快照少 1)。

## 已验证生效(2026-07-08)

`type cd` 输出 `cd is a shell builtin`(gvm cd 钩子已卸),带 `cd` 的命令(如 `cd /data/workspace/trpc-agent-go && git log`)不再报 `_encode/_decode`,无需再 `grep -vE "_encode|_decode"` 过滤。

**注意**:同一 tclaude 会话若从旧快照续用(如 `/resume` 旧会话),守卫不生效——会话复用的是守卫生效**前**的旧快照。需开新会话或 `/branch` 触发快照刷新,才会用上含守卫的新快照。

## 影响范围(已确认)

- 交互终端是 zsh(走 `~/.zshrc`,无此守卫),gvm 按目录自动切 Go 版本在真实终端照常工作,不受影响。
- Claude Code 的 bash 快照里 gvm 的 PATH/Go 仍在,只是没了 `cd` 钩子;手动 `gvm use` 仍可用,`go` 命令不受影响。
- `_encode`/`_decode` 在快照里仍被过滤丢弃(无法本地根治),但因无人调用,不再报错。

## 彻底根治(需上游)

彻底根治需 Anthropic 上游把 `ShellSnapshot.ts` 的 `^_[^_]` 启发式换成按 `complete -F` 注册表过滤(只丢真正的补全函数,不误伤 `_encode`/`_decode` 这类普通单下划线函数)。本地无解,守卫是当前最优缓解。
