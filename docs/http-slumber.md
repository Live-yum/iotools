# HTTP / Slumber 集合、模板与历史（中文）

核对对象为用户分支 [Live-yum/slumber](https://github.com/Live-yum/slumber)，
接口基准 `895fd49d8f51194d2def64b666e7188b6afec7a7`。这是独立 Go 实现，
不启动 Rust、Python、jq、curl 或 sqlite3 进程。

## 直接打开与导入

```sh
iotools --file slumber.yml --validate
iotools --file slumber.yml --profile local
iotools --import slumber --input slumber.yml --file converted.yaml
iotools --import rest --input api.http --file converted.yaml
iotools --import insomnia --input export.json --file converted.yaml
iotools --import openapi --input openapi.yaml --file converted.yaml
iotools --import v3 --input legacy.yml --file converted.yaml
```

- 原生 `version: 1` 格式与 Slumber v4/v5 的 `requests: {id: recipe}` 均能直接读取
- Slumber profiles 的 `data`、`default`，嵌套文件夹、全局唯一 ID、basic/bearer
  鉴权、query 重复参数、headers、raw/json/URL 表单/multipart 表单、`persist`
  及用户分支原生加解密字段均映射到统一请求模型
- 本地 `#/...` JSON Pointer、YAML anchor/alias、点前缀复用定义可用；引用循环、
  重复 ID、未知字段、多个 YAML 文档会报错；不会为 `$ref` 隐式访问网络
- 导入使用新文件且不覆盖；直接打开 Slumber 不会重写原文件。F4 编辑原 YAML，
  保存前校验并检查外部修改；F3 的原生表单编辑要求先导入为原生格式
- REST 支持变量、命名请求、headers 与 raw body；不执行其脚本/文件指令
- Insomnia 支持 resources 格式的环境、请求、鉴权、参数、正文与表单；脚本、
  未实现鉴权和隐式文件上传明确报错
- OpenAPI 支持 v3 JSON/YAML、server、operation、路径/query/header 参数、
  examples/defaults 与 basic/bearer/API key。无默认值的参数标为 `CHANGE_ME`；
  导入后先审阅环境与示例正文。不会猜测 OAuth 登录流程或自动接受远程引用
- v3 导入转换 `!request`、`!folder`、body/auth YAML tags、named chains、
  env/file/request/header/prompt/select 来源、JSONPath/trim/sensitive；旧 command
  链明确拒绝。复杂导入不等于原工具全部运行时/插件功能兼容

## 模板函数

`{{ ... }}` 与已有 `${profile_key}` / `${env:NAME}` 可以共存。
profile 字段懒加载，支持字段间引用、环境变量、循环检查。未使用的 crypto
定义不会因为缺少生产环境密钥而导致本地请求失败。

支持字符串/bytes/null/boolean/number/array/object 字面量、嵌套函数、关键字参数
以及管道。管道结果传给下一函数的最后一个位置参数，例如：

```yaml
profiles:
  local:
    default: true
    data:
      host: http://127.0.0.1:18080
      token: "{{ response('login', trigger='no_history') | jq('.token') | sensitive() }}"
requests:
  login:
    method: POST
    url: '{{ host }}/login'
    persist: false
    body:
      type: form_urlencoded
      data:
        username: "{{ env('DEMO_USER') }}"
        password: "{{ env('DEMO_PASSWORD') }}"
  data:
    method: GET
    url: '{{ host }}/data'
    authentication: {type: bearer, token: '{{ token }}'}
```

内建函数：

- `base64(value, decode=false)`、`env(name, default=...)`、`file(path)`
- `boolean/float/integer/string`、`concat`、`join`、`split`、`index`、`slice`
- `lower/upper/trim/replace`，其中 replace 支持 Go/RE2 正则和替换次数
- `json_parse`、`jsonpath`、`jq`，查询支持 `mode='auto'|'single'|'array'`
- `response`、`response_header`、`prompt`、`select`、`sensitive`、`debug`
- 用户分支 `encode/decode/encrypt/decrypt`，调用已有加解密实现而非另建编码器

`debug` 不把秘密打印到日志。模板预览不执行函数、不读文件、不触发请求。
`command()` 与分支便携模式一致禁用，避免外部运行时依赖和打开集合时执行命令。
TUI 为 prompt/select 提供交互；纯 CLI 未提供输入回调时明确报错。

JSON 正文内仅有一个动态片段时保留其类型，例如 `"{{ [1, 2] }}"` 会发出数组，
不会变成数组字符串。`string()` 可强制字符串。保留大整数，拒绝 JSON 后的
额外内容。二进制 file/bytes 可作为 raw 或 multipart 数据；URL/header 要求 UTF-8。
文件相对路径以集合所在目录为基准。单文件与请求/响应正文上限为 4 MiB；当前
`type: stream` 在这个上限内缓冲，尚不是任意大文件的流式上传。

## 请求链与确认

`response('id')` 默认只读取历史，绝不暗中发送请求。
`trigger='no_history'` / `'always'` / `'5m'` / `'1d'` 控制刷新；同次执行中重复依赖
复用结果，循环与过深链会报错。`response_header` 的 header 名称不区分大小写。
`view='raw'` 是原始网络 bytes；`view='transformed'` 是当前配方的派生解密视图，
与原始历史分开。

- 主请求与每个依赖写操作分别受授权约束
- TUI 在模板完整渲染后显示确切 method/目标，拒绝与取消不会发送该请求
- CLI 链式写操作要求同时指定 `--allow-writes --allow-chain-writes`
- `--read-only` 不允许链式写操作；仅确认主请求不会扩大为所有依赖写权限
- GET 是否会改变服务端状态由服务接口决定，不能仅凭 HTTP 方法识别所有业务副作用

## 查询与 SQLite 历史

默认不创建历史数据库。需要持久化时显式启用：

```sh
iotools --file slumber.yml --history-db ./history.sqlite --run data
iotools --history-db ./history.sqlite --history-query 'SELECT recipe, status, created_at FROM http_history ORDER BY id DESC LIMIT 20'
```

历史包含响应 headers/body 与派生视图，可能含令牌或个人数据；不要提交、同步或
公开数据库。数据库以私有权限创建，按集合绝对路径、profile、recipe 隔离。
`persist: false` 保留本次请求链内存缓存但不写数据库。Windows 文件 ACL 仍由
系统账户/目录权限控制，POSIX 的 `0600` 不能代替 Windows ACL 审计。

F7 可查询当前 HTTP JSON（jq）或显式启用的历史数据库。原生参数
`query_filter: '.items[] | select(.enabled) | .name'` 也可生成独立 `query` 事件。
查询不会改变原始响应，转换后的 JSON 存在时以转换视图为输入。

jq 使用纯 Go gojq；JSONPath 使用 RFC 9535 实现。两者在进程内执行，不依赖外部
命令。与原 Slumber 的 jaq 可能存在正则/边界语义差异，不声称逐字节等价。
查询结果最多 10000 项、4 MiB，可取消；SQL 查询最多 1000 行、4 MiB、5 秒。
SQL 控制台只接受单条 SELECT/WITH/EXPLAIN，在 SQLite 只读连接上执行，不开放
DELETE/UPDATE/ATTACH/PRAGMA。它不是任意修改数据库的 sqlite3 shell。

## 当前明确边界与验收

仍需逐项补齐/验收的原工具语义包括：所有外部格式边缘情况、更复杂导入格式、
无限流式上传、原 CLI 输出/历史管理命令、Python 包接口、系统
编辑器/剪贴板等平台交互，以及 jaq 与 gojq 的全部语言边界。导入限制会显式报错；
不把这些限制算成“全功能已完成”。

测试覆盖：真实回环 HTTP 登录链与拒绝、确切渲染目标确认、表单/重复 query、
原始与派生响应、typed JSON/binary file/multipart、lazy crypto、profile/请求环、
导入与本地引用、历史持久化隔离/persist=false、只读 SQL、jq 大整数与取消。
所有网络测试只用回环服务器，不含真实账户、生产端点或密钥。


## 已对源码补齐的组合与curl导出

核对fork `895fd49` 的 `crates/util/src/yaml/resolve.rs`：$ref支持当前文件和本地文件，
不是HTTP下载。现支持 `./other.yml#/path`（相对引用者目录）、绝对路径、~/路径、
标量/数组下标引用、跨文件嵌套、循环拒绝，以及$ref出现位置决定的覆盖顺序：
前面的字段可被引用覆盖，后面的字段覆盖引用。打开文件及F4保存不会发送网络请求。
单文件4MiB、所有引用16MiB、64文件/64层，超限明确报错。原生version:1的JSON正文
里的$ref仍是普通数据，不会被当成集合引用。F4只保存正在编辑的主文件。

核对 `crates/core/src/http/curl.rs` 与 `crates/cli/src/commands/generate.rs`：
`--curl 请求ID` 或F7生成当前HTTP请求的POSIX curl命令，保留query、headers、
请求体/加密变换、身份及TLS文件选项；程序不会执行这个命令。默认禁止触发依赖请求，
CLI可明确加 `--execute-triggers`，链式修改仍需独立write授权。引号安全转义；NUL/
非UTF-8二进制不能作为shell参数，明确要求导出为文件。生成结果可能含机密，请勿公开。

## 请求历史管理

F11显示当前集合的历史，Enter查看完整原始/派生响应，Ctrl-Y明确复制。
按D必须输入匹配的历史ID再确认永久删除；取消或ID不匹配不改数据库。
CLI支持 --history-list、--history-get ID、--history-delete ID1,ID2，并要求明确
--history-db；删除另需 --allow-history-delete。命令必须同时选定集合 --file。
操作按集合绝对路径隔离，跨集合/缺失ID使整批回滚；没有空列表“全部删除”。
历史查询用只读连接，删除使用单个事务；后台执行不会阻塞订阅界面，退出会取消。
读取二进制响应保留raw_body_base64，不用替换字符损失原始字节。
