---
name: patch-push
description: |
  使用 patchpush 将某个 Jira 的开发分支 cherry-pick 并推送到目标分支（main、qa、release 分支、master 等）。
  适用于从分支名解析 jira_id 与描述、创建或更新 config.yaml.<jira>、预检本地仓库、先 dry-run 再执行
  ./patchpush push -c <config>、处理 cherry-pick 冲突与断点续跑（-continue/-abort）、用 pull 重建目标分支并汇总 MR 链接。
  触发词：推送 patch、patchpush、config.yaml.123、PROJ-xxxx 推送、cherry-pick 到 release 分支、force-with-lease。
---

# Patch Push

按「确认输入 -> 解析 -> 配置 -> 预检 -> dry-run -> 逐库执行 -> 冲突/断点处理 -> 验证交付」执行。
不要跳过预检和 dry-run；不要在冲突时擅自 skip/abort。

> 工具二进制名为 `patchpush`。请使用最新构建，避免旧二进制。

## 0. 输入门禁

开始前必须明确以下 4 项，缺任一项先问，不要猜：

1. 开发分支完整名，例如 `PROJ-123_example_feature_dev`。
2. 目标分支，例如 `main`、`qa`、`release-1.0`，可多个。
3. 要推送的 repo key，即 config 中 `repo:` 下的 key，例如
   `service-a`、`service-b`、`service-c`。
   若 config 已写 `patch.repos`，可不传 `-r`；命令行 `-r` 优先于 config。
4. 是否允许真实 push。工具不会删除远端分支，但会：
   - 在 `-m n`（默认）下**删除并重建本地集成分支**；
   - 用 `--force-with-lease` 更新远端同名集成分支；
   - 推送新分支到 origin。
   必须得到明确授权后才能去掉 `-dry-run`。

默认先用 `-dry-run` 看完整计划；真实执行时优先逐个 repo（`-r <repo>`）以便定位冲突，熟练且无冲突风险时再用 `patch.repos` 或 `-r a,b` 一次跑完。

## 1. 工具与工作目录

项目根：当前仓库根目录。运行工作目录：`./nbin`（config、日志、报告、断点都按当前目录相对路径生成）。

首次使用或代码更新后必须重新构建，避免旧二进制：

```bash
cd <repo-root>
make build                    # 生成 ./patchpush
cp -f patchpush nbin/patchpush
nbin/patchpush -version       # 确认版本对应最新提交
```

也可直接使用 `dist/patchpush-linux-amd64`，但同样要确认版本。

`push_report.txt`、`patchpush.log`、`<config>.state` 都在执行命令的当前目录生成；因此固定进 `nbin` 再执行。

## 2. 解析分支

- `jira_id`：分支开头的 `<KEY>-<数字>`，例如 `PROJ-123`、`PROJ-456`。不限定前缀。
- `jira_desc`：去掉 `jira_id` 前缀和**一个**尾部环境后缀。
  - 环境后缀常见：`_lo`、`_local`、`_dev`、`_staging`。
  - `PROJ-123_example_feature_dev` -> `example_feature`
  - `PROJ-124_example_fix_local` -> `example_fix`
  - `PROJ-125_example_change_dev` -> `example_change`
- `dev_branch`：保留**完整分支名**，不改写、不去后缀。
- 集成分支由工具生成：`<jira_id>_<jira_desc>_<tgt_branch>`，例如 `PROJ-123_example_feature_main`。
- `jira_desc` 只允许字母、数字、`.`、`_`、`-`，不能有空格和特殊字符。

## 3. 创建/更新 config

工作目录：`./nbin`

- 文件名：`config.yaml.<jira_id 的数字部分>`。`PROJ-123` -> `config.yaml.123`。
- 若同名文件已存在，先读取确认是复用还是更新；**不要静默覆盖**。
- 历史遗留例外：`config.yaml.PROJ-456` 带项目前缀。不要因为存在例外就改变默认命名规则。
- 以 `config.yaml`（或最近一次 `config.yaml.<n>`）为模板复制；`repo:` 段固定，路径以本机实际 clone 为准，**不要改**。
- 在 `patch:` 段写入本次信息：

```yaml
patch:
  dev_branch: PROJ-123_example_feature_dev
  tgt_branchs:
    - main
  jira_id: PROJ-123
  jira_desc: example_feature
  repos:            # 可选；写了就不必每次传 -r
    - service-a
    - service-b
```

- 可选的默认参数段（命令行显式参数优先）：

```yaml
push:
  model: n          # n 重建本地集成分支（默认），a 复用已有分支
  dry_run: true     # 建议先置 true，确认后再改 false
  yes: false        # true=冲突时不询问，保存断点退出
  continue: false
  abort: false

pull:
  delete: false     # pull -d 的默认值
```

- 写完后校验：
  - `patch.jira_id`、`patch.jira_desc`、`patch.dev_branch`、`patch.tgt_branchs` 必填；
  - `tgt_branchs` 不能包含 `dev_branch`；
  - `patch.repos` 里的 key 必须存在于 `repo:`；
  - 与模板对比，除 `patch:`、`push:`、`pull:` 外内容一致。
- `repo.url` 必须是 http(s)，用于拼 MR；`repo.path` 必须是本地已有 git 工作区。

## 4. 预检

对每个待推送 repo 的 `path`（以 config 为准）执行：

```bash
P=<repo.path>
git -C "$P" rev-parse --is-inside-work-tree                      # 必须是 true
git -C "$P" rev-parse --abbrev-ref HEAD                          # 允许任意分支；工具会记录并恢复
git -C "$P" status --porcelain --untracked-files=no              # 必须无输出；未跟踪文件可忽略
git -C "$P" rev-parse --verify refs/heads/<dev_branch>           # 开发分支必须存在于本地
git -C "$P" rev-parse --verify refs/heads/<tgt_branch> \
  || git -C "$P" ls-remote --heads origin refs/heads/<tgt_branch> # 本地或远端至少存在一个
git -C "$P" ls-remote >/dev/null                                 # 远端必须可达
```

再用与工具相同的算法确认会选中哪些 commit：

```bash
# 本地有目标分支就用分支名，否则先 fetch 用 origin/<tgt_branch>
TGT=<tgt_branch>   # 或 origin/<tgt_branch>
git -C "$P" cherry -v "$TGT" <dev_branch> | grep -Ei '^[+-] [0-9a-f]+ (\[?<jira_id>(\]|:|：))'
git -C "$P" cherry -v "$TGT" <dev_branch>
```

判读规则：

- `+`：待 cherry-pick；`-`：改动已通过 patch-id 等价进入目标分支，会在结果里记为 `already applied`。
- 只保留 commit message 以 `<jira_id>:`、`<jira_id>：` 或 `[<jira_id>]` 开头的提交，大小写不敏感。
- 已被后续 `Revert "<原提交标题>"` 撤销的提交会被自动排除。
- merge commit 会被跳过（不进入候选）。
- 全部提交都是 `-` 时是 **NO-OP**：不建集成分支、不 push、不生成 MR，也不算失败。
- 工具不会自动更新开发分支；若远端 dev 分支有新提交，预检前先手动 fetch/pull 本地 dev 分支。
- 跨库提交要确认共享依赖也在本次推送范围内。例如 service-b 依赖 service-a 的常量/函数时，service-a 必须一起推送。

预检失败先修复或报告，不要带病 push。预检只是执行前快照：工具随后会先 `pull --ff-only` 目标分支，最终选中的 commit 以 `-dry-run` 或实际输出为准。

## 5. 执行 push

先 dry-run（不修改工作区、不推送，只打印 commit 计划和 MR URL）：

```bash
cd ./nbin
./patchpush push -c config.yaml.<n> -dry-run
# 或只跑部分 repo
./patchpush push -c config.yaml.<n> -r <repo> -dry-run
```

确认 commit 列表、目标分支、集成分支名无误后再真实执行：

```bash
./patchpush push -c config.yaml.<n>
./patchpush push -c config.yaml.<n> -r <repo>          # 单库执行，冲突更易定位
./patchpush push -c config.yaml.<n> -m a               # 复用已有集成分支而不是重建
```

若 config 里写了 `push.dry_run: true`，真实执行时用 `-dry-run=false` 覆盖；不要把 `push.continue`/`push.abort` 预置为 `true`，否则每次运行都会走续跑/放弃逻辑。

关键参数：

| 参数 | 说明 |
| --- | --- |
| `-r` | repo key，逗号分隔；省略时用 `patch.repos` |
| `-c` | 配置文件，默认 `config.yaml` |
| `-m n\|a` | `n` 删除并重建本地集成分支（默认）；`a` 复用本地/远端已有集成分支 |
| `-dry-run` | 只打印计划，不做任何修改或推送 |
| `-yes` | 非交互：遇到冲突不询问，直接保存断点并退出（之后用 `-continue`） |
| `-continue` | 解决冲突后从中断处继续；已完成的 repo/分支会跳过 |
| `-abort` | 放弃上次中断的 cherry-pick 并清理断点 |

工具流程（每个 repo × 每个目标分支）：

1. 校验工作区干净、dev 分支存在、origin 可达。
2. 切目标分支并 `git pull --ff-only`；本地没有则从 origin 创建。
3. 用 `git cherry -v <target> <dev>` 选 commit，过滤 Jira 前缀、已应用和 Revert。
4. 创建/复用集成分支 `<jira>_<desc>_<tgt>`。
5. 按时间从旧到新 cherry-pick。
6. `git push --set-upstream origin <new_branch>`；远端已存在时使用 `--force-with-lease`。
7. 打印并修正 MR 链接（强制写入 `merge_request[target_branch]`）。
8. 成功或 NO-OP 时切回执行前分支；结果写入 `push_report.txt`。

交互注意：

- 只有发生冲突且未加 `-yes` 时才需要终端输入；普通 bash 管道可能 EOF，需用 tmux 等交互终端。
- 提示是**按行读取**的：输入 `y` 后必须按 Enter。

## 6. 冲突与断点

看到 `cherry-pick conflict` 时，工具会等待：

- `y`：冲突已解决，工具执行 `git cherry-pick --continue` 并继续后续提交。
- `s`：跳过当前提交。
- `n`：停止并保存断点，退出码非 0。

默认**停下分析**，不要自动 `s` 或 `n`。先收集：

```bash
P=<repo.path>
git -C "$P" status
git -C "$P" diff
git -C "$P" diff --name-only --diff-filter=U
git -C "$P" log --oneline -5
```

解决原则：

1. 以**目标分支当前代码**为基准，保留目标分支已有的重构、删除和新增。
2. 把开发提交的**意图**重新应用上去，不要整文件 `git checkout --theirs`。
3. import 冲突通常两边符号都保留。
4. 目标分支已经删除的 hook/函数，不要因为开发分支旧版本而复活。
5. 合并后检查未定义变量（例如旧结构的 `cpu_model_ignore` 是否被新结构移除）。

验证与继续：

```bash
python3 -m py_compile <changed>.py
git -C "$P" add <changed files>
GIT_EDITOR=true git -C "$P" cherry-pick --continue
```

然后二选一：

- 仍在交互提示中：输入 `y` + Enter；
- 已退出（例如用了 `-yes`）：用同样参数加 `-continue` 重跑：

```bash
cd ./nbin
./patchpush push -c config.yaml.<n> -continue
```

断点保存在 `<config>.state`（JSON），记录已完成的 repo/分支和当前 job 进度；**成功完成后自动删除**。不要手工删除正在续跑的 `.state`。

想彻底放弃：

```bash
./patchpush push -c config.yaml.<n> -abort
```

`-abort` 只会 abort 当前 job 的 cherry-pick 并清理断点，不会删远端分支。

如果冲突涉及业务语义且无法判断，停止并报告：冲突文件、双方差异摘要、建议方案，等用户决定。

## 7. pull（目标分支重建）

目标分支（如 `qa`）在远端被重建、本地 `pull --ff-only` 失败时使用：

```bash
cd ./nbin
./patchpush pull -c config.yaml.<n>
./patchpush pull -c config.yaml.<n> -d     # 先删本地目标分支，再基于 origin 重建
```

- `-d` 只删除**本地**目标分支，不会删除远端。
- `pull` 会处理 `patch.tgt_branchs` 中的所有目标分支；`-r` 可选，省略时用 `patch.repos`。
- 不要对 `dev_branch` 执行 pull（工具会拒绝）。

## 8. 验证

push 完成后逐项核对：

```bash
P=<repo.path>
cd ./nbin
test -f config.yaml.<n>.state && echo PENDING || echo clean

git -C "$P" rev-parse --abbrev-ref HEAD                         # 成功时应回到执行前分支
git -C "$P" rev-parse HEAD
git -C "$P" ls-remote --heads origin refs/heads/<new_branch>    # 远端 tip
git -C "$P" rev-list --count HEAD ^origin/<new_branch>          # 期望 0
git -C "$P" rev-list --count origin/<tgt_branch>..HEAD          # 期望等于本次 commit 数
git -C "$P" diff --stat origin/<tgt_branch>..HEAD
```

- 远端 tip 必须等于本地 tip。
- 本地 commit 的父链应回到目标分支：单 commit 时 `HEAD^ == origin/<tgt>`；多 commit 时依次回溯。
- 无 `CHERRY_PICK_HEAD` 残留；已跟踪文件干净。
- Python 改动跑 `py_compile`。
- MR 链接以本次 stdout 的 `RESULT SUMMARY` 为准；`push_report.txt` 会累积历史，不要用它判断本次是否成功。
- 失败时工具可能停在目标分支或集成分支上（早退路径不保证恢复），继续操作前先确认当前分支。

## 9. 交付格式

输出表格：库 / 目标分支 / 状态（OK / NO-OP / DRY-RUN / FAILED）/ commit 数 / 集成分支 tip / MR URL。

再补充：

- 本次创建的 config 文件名与实际使用的二进制版本。
- 是否发生冲突、冲突文件、解决方式。
- NO-OP 的 repo/分支（说明提交已全部应用）。
- 任何需要用户注意的风险：
  - 远端集成分支被 `--force-with-lease` 更新；
  - 本地集成分支在 `-m n` 下被删除重建；
  - 执行失败后仓库当前分支可能未恢复。

## 10. 已知坑

- 必须先 `make build` 并替换 `nbin/patchpush`；请使用最新构建的二进制。
- config 里的 `repo.path` 是本地已 clone 仓库，路径各不相同，一律以 config 为准。
- 工具不删除远端分支；不要沿用旧流程里「删远端同名分支」的描述。
- `-m n` 只删除**本地**集成分支；保护和拒绝删除的分支包括 `master`、`main`、`qa`、`staging`、`prod`、`release`、`release/*`。
- `--force-with-lease` 绑定执行时观测到的远端 SHA；若他人刚更新了集成分支，push 会失败，这是保护行为，不要改用 `--force`。
- 工具成功或 NO-OP 会切回原分支；失败/中断时可能停留在目标分支或集成分支，需人工确认。
- 未跟踪文件不影响切分支；已跟踪文件有改动会直接预检失败。
- `-r` 支持逗号分隔多 repo；真实执行前建议先整批 `-dry-run`，再按需逐库执行。
- 分支名里的环境后缀只用于解析 `jira_desc`；`dev_branch` 仍使用完整分支名。
- `patch.repos` 可省去每次传 `-r`，但命令行 `-r` 优先；两者都为空会报 `param repo is none`。
- `-yes` 只是把冲突处理变成「保存断点退出」，不会自动解决冲突；解决后仍需 `-continue`。
- MR 链接由工具改写 `target_branch`，不要手工拼接或改回默认分支。
- `push_report.txt`、`patchpush.log`、`*.state` 都在当前工作目录；固定从 `nbin` 执行。
- `patchpush.log` 自动轮转（默认 10MB，保留 3 份）；`push_report.txt` 不轮转，会持续增长。
