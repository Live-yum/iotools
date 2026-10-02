# 内置历史集合管理和 SQL 控制台

F7点“历史管理”，或F11历史表按M。只操作明确填写的本机历史数据库，不连接服务器。
可直接编辑SQL，默认“列出全部集合”；普通F7 SQL查询继续只读。

## 集合操作
- 全库列表包含集合标识、记录数与ID范围；标识通常是集合文件绝对路径
- 删除仅移除指定集合的历史，不删除YAML文件
- 迁移合并只改历史所属集合，保留ID、正文与时间，不覆盖目标已有历史
- 填来源/目标/新备份文件，先预览实际SQL和数据库路径，再明确执行
- 任意集合/recipe/profile/最新记录/ID列表可用只读SQL查询，F11仍提供当前集合逐ID查看

## 多语句 SQL
支持SELECT/WITH/EXPLAIN、INSERT/UPDATE/DELETE/REPLACE、普通CREATE/DROP/ALTER。
.tables列出表，.schema显示定义。支持字符串中的分号、注释和可选外层BEGIN/COMMIT。
最多32语句/64KiB，工具持有一个事务，不允许中途COMMIT逃逸。
只读结果最多1000行/4MiB/5秒，可写脚本最多10秒；备份与提交后数据库最多64MiB。

禁止ATTACH/DETACH、VACUUM INTO、扩展加载、任意PRAGMA、虚拟表和触发器，
避免SQL访问其他文件或将隐藏副作用留给未来请求。不依赖外部sqlite3。
只读模式优先，不能通过集合操作、可写SQL或F11删除绕过。

## 预览、备份与事务
预览只读一致快照，令牌绑定绝对路径、SQL和当前数据库内容。
执行时取得SQLite写锁重新核对；任何变化均拒绝，须重新预览。
在任何SQL之前，完整原库快照写入新的0600备份文件并sync；禁止覆盖已有文件/符号链接。
备份失败不执行；任一句失败/超时/超限整批回滚，备份保留。WAL已提交数据也纳入备份。
成功结果返回备份绝对路径。不要将含敏感响应的数据库或备份提交Git。

恢复时先关闭使用该库的程序；保留当前库和WAL文件，把备份复制为新数据库路径，
用 --history-db 新路径 打开验证，不必永久删除旧文件。

## CLI
只读查询不需要加载请求集合：
- iotools --history-db history.sqlite --history-collections
- iotools --history-db history.sqlite --history-script ".tables"
- iotools --history-db history.sqlite --history-script "SELECT collection,recipe,status FROM http_history ORDER BY id DESC LIMIT 20; SELECT count(*) FROM http_history"

先预览，不修改：
- iotools --history-db history.sqlite --history-collection-action migrate --history-source old.yaml --history-target new.yaml
- iotools --history-db history.sqlite --history-script "UPDATE http_history SET collection='new.yaml' WHERE collection='old.yaml'" --history-preview

随后原样重复同一操作，提供预览JSON的token、新备份路径和明确写授权：
--history-apply TOKEN --history-backup before-change.sqlite --allow-writes
库写入新历史后旧token会拒绝，需重新预览；--read-only始终拒绝执行。

测试仅用临时合成数据库，覆盖跨集合迁移/删除、预览失效、WAL备份完整性、
失败回滚、只读拒绝、重复点击一次执行、取消不变更、新文件不覆盖。
