# patch-push (patchpush)

`patchpush` 是一个批量推送 patch 的命令行工具：以**一个 Jira 为一次提交的中心**，把一个开发分支中属于该 Jira 的提交，自动 cherry-pick 到多个仓库、多个目标分支，并生成 GitLab Merge Request 链接。

它能做到：

- 一条命令完成「切目标分支 → 拉最新 → 建集成分支 → 按 Jira 过滤提交 → 倒序 cherry-pick → push → 打印 MR 链接」。
- 同时处理多个 repo、多个目标分支。
- 自动跳过已被后续 `Revert` 提交撤销的提交。
- cherry-pick 冲突时暂停，人工处理后继续；支持 skip 和退出后断点续跑。
- 默认只做**安全推送**：不再删除远端同名分支，使用 `--force-with-lease` 更新。
- 结束时自动切回执行前的分支。
- 把本次结果（提交清单、MR 链接、失败原因）追加写入 `push_report.txt`，调试日志写入 `patchpush.log`（自动轮转）。

## 背景

每次任务涉及多个 repo 时，自测要 cherry-pick 到 test，QA 要 cherry-pick 到 qa，上线又要到 staging……逐个 repo、逐个分支操作非常耗时。此工具把重复劳动收敛成一次配置 + 一条命令。

## 构建

```bash
make build          # 生成 ./patchpush
make test           # 运行测试
make vet            # go vet
make lint           # staticcheck（需自行安装）
make release        # 交叉编译到 dist/
```

需要 Go 1.21+。

## 配置

默认读取当前目录的 `config.yaml`，也可用 `-c` 指定。

```yaml
patch:
  dev_branch: PROJ-123_example_feature_dev  # 开发分支（完整分支名）
  tgt_branchs:                              # 目标分支，可多个
    - qa
    - staging
  jira_id: PROJ-123
  jira_desc: example_feature                # 简短描述，只允许字母/数字/._-，用于分支名
  # 默认要操作的 repo，等价于命令行 -r；命令行 -r 优先。
  repos:
    - service-a
    - service-b

# push 默认参数，等价于对应的命令行参数；命令行显式参数优先。
push:
  model: n          # -m
  dry_run: false    # -dry-run
  yes: false        # -yes
  continue: false   # -continue
  abort: false      # -abort

# pull 默认参数
pull:
  delete: false     # -d

repo:
  service-a:
    name: service-a
    url: https://git.example.com/team/service-a/
    path: /home/me/repos/service-a # 本地已 clone 的仓库
  service-b:
    name: service-b
    url: https://git.example.com/team/service-b/
    path: /home/me/repos/service-b
```

- `repo` 下的 key 就是 `-r` 使用的名称。
- `url` 必须是 http(s) 地址，用于生成 MR 链接。
- `path` 必须是本地已有、干净的 git 工作区。
- 集成分支名由工具生成：`<jira_id>_<jira_desc>_<tgt_branch>`。

配置会在启动时校验：必填项缺失、分支名非法、`dev_branch` 与目标分支相同、`jira_desc` 含空格等情况会直接报错退出。

### 参数优先级

`-r`、`-m`、`-dry-run`、`-yes`、`-continue`、`-abort`、`-d` 都可以写在 `config.yaml` 里作为默认值：
- `patch.repos`：push/pull 默认操作的 repo（等价于 `-r`）。
- `push`：`model`/`dry_run`/`yes`/`continue`/`abort` 的默认值。
- `pull`：`delete` 的默认值。

**命令行显式传入的参数优先于 config**。工具通过 `flag.FlagSet.Visit` 判断哪些参数是命令行真正传入的，只对未传入的参数套用 config 默认值。`-c`（配置文件路径本身）是定位配置的入口，不能写在配置里。

## 命令

### push

```bash
./patchpush push -r service-a,service-b -c config.yaml
```

| 参数 | 说明 |
| --- | --- |
| `-r` | 要推送的 repo 名称，逗号分隔 |
| `-c` | 配置文件路径，默认 `config.yaml` |
| `-m` | 集成分支模式：`n` 重建（默认）、`a` 复用已有本地分支 |
| `-dry-run` | 只打印将要 cherry-pick 的提交和计划，不做修改、不推送 |
| `-yes` | 非交互模式：遇到冲突时不询问，直接保存断点并退出（随后可用 `-continue` 继续） |
| `-continue` | 解决冲突后，从中断处继续 |
| `-abort` | 放弃上次中断的 cherry-pick |

执行流程：

1. 校验配置、仓库是否干净、dev 分支是否存在、origin 是否可达。
2. 切到目标分支并 `git pull --ff-only`；本地没有则从 `origin` 创建。
3. 用 `git cherry -v <target> <dev>` 计算提交：`+` 为待 cherry-pick，`-` 表示改动已通过 cherry-pick 等价进入目标分支（SHA 不同，patch-id 相同）。只保留 commit message 以 `jira_id` 开头的提交，并自动跳过已被后续 `Revert` 撤销的提交。
4. 如果该 Jira 的提交全部已应用，则记为 **NO-OP**：不创建集成分支、不 push、不生成 MR，也不计为失败。
5. 按时间从旧到新 cherry-pick（merge commit 不会进入候选列表）。
6. 用 `git push --force-with-lease` 推送集成分支（远端不存在则普通推送）。
7. 生成并打印 MR 链接；切回执行前分支。
8. 结果追加写入 `push_report.txt`。

### pull

用于目标分支（如 `qa`）在远端被重建的场景。

```bash
./patchpush pull -r service-a,service-b          # 切到目标分支并 ff-only 拉取
./patchpush pull -r service-a,service-b -d       # 先删本地目标分支，再基于 origin 重建
```

`-d` 只删除**本地**分支，不会删除远端。

## 冲突处理

发生冲突时工具会暂停并提示：

- `y`：已解决冲突（如尚未执行 `git cherry-pick --continue`，工具会代为执行）。
- `s`：跳过当前提交（`git cherry-pick --skip`）。
- `n`：停止并保存断点。

选择 `n` 或进程被中断后：

1. 手动解决冲突。
2. 用同样的参数加 `--continue` 继续。
3. 想彻底放弃时用 `--abort`。

断点信息保存在 `<config 路径>.state`（例如 `config.yaml.4135.state`），完成后自动删除。

## 提交规范

- commit message 必须以下列形式开头，否则不会被选中：
  - `ABC-1: 描述`
  - `[ABC-1] 描述`
  - 冒号支持半角 `:` 和全角 `：`，Jira ID 大小写不敏感。
- 同一 Jira 在不同 repo 中建议使用相同的开发分支名。
- `jira_desc` 不要含空格或特殊字符。

## 安全说明

- 更新远端集成分支使用 `--force-with-lease`，只在本工具确认的远端 SHA 上强推，避免覆盖他人提交。
- 不会删除远端 `master`/`main`/`qa`/`staging`/`prod`/`release/*` 等受保护分支；集成分支被删除重建时也会先校验保护名单。
- 执行前要求工作区无未提交的已跟踪改动，避免切分支失败留下半完成状态。
- `-dry-run` 可先观察将执行的提交，确认无误再实际推送。

## 报告与日志

- `push_report.txt`：每次运行追加一个带时间戳的区块，包含 repo、分支、待应用/已应用提交、MR 链接和错误；全部已应用时标记 no-op。该文件会累积历史，请以本次 stdout 输出为准。
- `patchpush.log`：调试日志，自动按大小轮转（默认 10MB，保留 3 份）。可用 `-version` 查看版本。

## 已知限制

- merge commit 会被跳过（`git cherry-pick` 需要 `-m`，工具暂不自动处理）。
- “已应用”依赖 patch-id 判断：若当初 cherry-pick 解决冲突导致 diff 改变，可能仍被当作未应用；若应用后又被 revert，patch-id 仍存在，可能被当作已应用。
- `pull` 使用 `--ff-only`；本地目标分支与远端分叉时会失败，请用 `-d` 重建。
- MR 链接优先从 push 输出解析，并强制把 `target_branch` 改写为本次推送的目标分支（否则 GitLab 会回落到默认分支）；解析不到时按配置 `url` 拼接。
- 工具只生成 MR 链接，不会自动创建 MR。

## 开发

```bash
make test
make vet
make lint
```

测试包含真实临时 git 仓库的集成用例：提交过滤、全角冒号、倒序 cherry-pick、force-with-lease、dry-run、冲突 skip、报告追加等。

欢迎提交 MR 或 issue。
