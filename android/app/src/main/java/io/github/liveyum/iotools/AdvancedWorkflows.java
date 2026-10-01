package io.github.liveyum.iotools;

import android.app.AlertDialog;
import android.content.Context;
import android.os.Handler;
import android.os.Looper;
import android.text.InputType;
import android.view.View;
import android.view.ViewGroup;
import android.widget.*;
import org.json.*;
import java.time.Instant;
import java.util.*;

/** Explicit native workflows for the finite mobile service operations. */
final class AdvancedWorkflows {
    interface Callback { void accept(Object data); }
    interface Host {
        void call(JSONObject command, Callback success);
        JSONObject request() throws JSONException;
        List<JSONObject> events();
        void show(String title, Object data);
        void exportText(String name, String text);
        void prepare(JSONObject request);
        void start(JSONObject response);
        void stateChanged(JSONObject state);
    }
    private final Context context;
    private final Host host;
    private final NativeUi ui;
    private final Handler handler = new Handler(Looper.getMainLooper());
    private JSONObject baseline;
    private String controllerRun = "", discoveryRun = "";
    private int discoveryPort = 502;

    AdvancedWorkflows(Context context, Host host) {
        this.context = context; this.host = host; this.ui = new NativeUi(context);
    }

    void addTo(LinearLayout parent) {
        LinearLayout opc = section(parent, "OPC UA · 连接与订阅", "独立订阅可与前台读取并行；离开前台会停止网络任务");
        action(opc, "管理独立订阅", this::subscriptions);
        action(opc, "成功连接记录", this::connections);
        action(opc, "生成客户端证书与私钥", this::identity);
        LinearLayout modbus = section(parent, "Modbus · 本机工作流", "快照与解码只处理已有数据。探测与控制服务必须明确启动");
        action(modbus, "网络探测 · 预览目标", this::discovery);
        action(modbus, "查看网络探测结果", this::discoveryResults);
        action(modbus, "寄存器快照 · 保存、载入与比较", this::snapshots);
        action(modbus, "CSV 快照比较", this::csvDiff);
        action(modbus, "导入寄存器标签、固定行与规则", this::importRegisters);
        action(modbus, "重新解释单次响应", this::interpret);
        action(modbus, "本机 HTTP 控制接口", this::controller);
        LinearLayout session = section(parent, "Modbus · 会话", "暂停保留当前任务；停止结束当前读取。统计来自实际引擎 I/O");
        LinearLayout controls = ui.row();
        ui.weighted(controls, ui.button("暂停读取", false, () -> invoke("modbus.pause", d -> host.show("读取已暂停", d))));
        ui.weighted(controls, ui.button("继续读取", false, () -> invoke("modbus.resume", d -> host.show("读取已继续", d))));
        session.addView(controls);
        action(session, "会话统计", () -> invoke("modbus.stats", this::stats));
        action(session, "查看写入审计文件", () -> chooseFile("选择写入审计文件", name -> host.call(json("op", "modbus.write-log", "path", name), data -> host.show("写入审计", data))));
        LinearLayout files = section(parent, "应用文件与集合", "文件位于应用私有目录；切换集合前会检查内容，且不会自动连接");
        action(files, "文件清单与集合切换", this::files);
        appendHistoryTools(parent);
    }

    void pause() { handler.removeCallbacksAndMessages(null); }

    void appendHistoryTools(LinearLayout parent) {
        LinearLayout card = section(parent, "HTTP 历史集合", "管理本机历史分组。重命名、合并、迁移与删除均先预览并创建新备份");
        action(card, "管理历史集合", this::historyCollections);
    }

    private void historyCollections() {
        invoke("history.collections", data -> {
            JSONArray rows = NativeUi.array(data); LinearLayout body = page();
            body.addView(ui.text("这些是历史数据库中的集合标识，不会更改当前请求文件", 13, NativeUi.MUTED));
            addRows(body, rows, row -> row.optString("collection"), row -> row.optString("requests") + " 条历史 · ID " + row.optString("first_id") + "–" + row.optString("last_id"), row -> historyCollectionActions(row, rows));
            dialog("HTTP 历史集合", body);
        });
    }

    private void historyCollectionActions(JSONObject row, JSONArray all) {
        LinearLayout body = page(); ui.pair(body, "来源集合", row.optString("collection")); ui.pair(body, "历史条数", row.optString("requests"));
        action(body, "重命名历史集合", () -> historyCollectionEdit("rename", "重命名", row, all));
        action(body, "合并到其他集合", () -> historyCollectionEdit("merge", "合并", row, all));
        action(body, "迁移到另一个集合标识", () -> historyCollectionEdit("migrate", "迁移", row, all));
        action(body, "删除此集合的历史", () -> historyCollectionEdit("delete", "删除", row, all));
        dialog("管理此历史集合", body);
    }

    private void historyCollectionEdit(String kind, String title, JSONObject row, JSONArray all) {
        LinearLayout form = page(); String source = row.optString("collection");
        ui.pair(form, "来源集合", source); ui.pair(form, "历史条数", row.optString("requests"));
        if (kind.equals("delete")) form.addView(ui.text("删除此集合的全部本机 HTTP 历史。执行前会生成完整数据库备份", 14, NativeUi.DANGER));
        else form.addView(ui.text("更新历史记录所属的集合标识。请求及响应内容保留；不会执行网络请求", 13, NativeUi.MUTED));
        EditText target = kind.equals("delete") ? null : field(form, kind.equals("rename") ? "新的集合标识" : "目标集合标识", "", false);
        if (target != null) action(form, "选择已有集合标识", () -> {
            ArrayList<String> names = new ArrayList<>(); for (int i = 0; i < all.length(); i++) { JSONObject other = all.optJSONObject(i); if (other != null && !source.equals(other.optString("collection"))) names.add(other.optString("collection")); }
            if (names.isEmpty()) { info("没有其他集合", "请手动输入新的目标集合标识"); return; }
            AlertDialog choose = new AlertDialog.Builder(context).setTitle("选择目标历史集合").setItems(names.toArray(new String[0]), (d, which) -> target.setText(names.get(which))).setNegativeButton("取消", null).create(); style(choose);
        });
        EditText backup = field(form, "新的数据库备份文件名", "history-" + kind + "-" + System.currentTimeMillis() + ".sqlite", false);
        submit(title + "历史集合", form, "检查影响范围", window -> {
            String destination = target == null ? "" : required(target), backupName = fileName(backup);
            if (!destination.isEmpty() && destination.equals(source)) throw new IllegalArgumentException("目标集合必须不同于来源集合");
            host.call(json("op", "history.collection.preview", "kind", kind, "source", source, "target", destination), result -> {
                if (!window.isShowing()) return;
                JSONObject preview = NativeUi.object(result); LinearLayout review = page();
                ui.pair(review, "操作", title); ui.pair(review, "来源", source); if (!destination.isEmpty()) ui.pair(review, "目标", destination);
                ui.pair(review, "来源历史数", row.optString("requests")); ui.pair(review, "备份文件", backupName); ui.pair(review, "备份大小", preview.optString("backup_bytes") + " 字节");
                review.addView(ui.text("只读模式会阻止此操作。数据库在预览后变化时需重新预览，已有备份文件不会覆盖", 13, NativeUi.MUTED));
                action(review, "查看执行语句", () -> host.show("历史集合操作语句", preview.optString("sql")));
                confirmView("确认" + title + "历史集合", review, "备份并" + title, () -> host.call(json("op", "history.execute", "sql", preview.optString("sql"), "token", preview.optString("token"), "backup", backupName, "confirmed", true), done -> {
                    window.dismiss(); JSONObject changed = NativeUi.object(done); LinearLayout summary = page();
                    summary.addView(ui.title("历史集合操作已完成", 19)); ui.pair(summary, "操作", title); ui.pair(summary, "备份", changed.optString("backup", backupName));
                    JSONArray results = changed.optJSONArray("results");
                    if (results != null) for (int i = 0; i < results.length(); i++) { JSONObject set = results.optJSONObject(i); if (set == null) continue; JSONArray columns = set.optJSONArray("Columns"), values = set.optJSONArray("Rows"); if (columns == null) columns = set.optJSONArray("columns"); if (values == null) values = set.optJSONArray("rows"); if (columns != null && values != null && values.length() > 0) { JSONArray first = values.optJSONArray(0); if (first != null) for (int c = 0; c < Math.min(columns.length(), first.length()); c++) ui.pair(summary, columns.optString(c), first.optString(c)); } }
                    action(summary, "刷新历史集合", this::historyCollections); dialog("历史管理结果", summary);
                }));
            });
        });
    }

    void addSubscriptionsTo(LinearLayout parent) {
        LinearLayout card = section(parent, "独立 OPC UA 订阅", "查看各订阅的节点、最新值与状态，并逐一停止");
        action(card, "打开订阅管理", this::subscriptions);
        action(card, "查看成功连接", this::connections);
    }

    void subscriptions() {
        LinearLayout body = page();
        body.addView(ui.text("订阅互相独立，最多同时运行 16 个。关闭此窗口不会停止订阅", 13, NativeUi.MUTED));
        LinearLayout buttons = ui.row();
        final AlertDialog[] window = new AlertDialog[1];
        ui.weighted(buttons, ui.button("添加订阅", true, () -> { window[0].dismiss(); addSubscription(); }));
        ui.weighted(buttons, ui.button("停止全部", false, () -> confirm("停止全部订阅", "结束所有独立 OPC UA 订阅。前台读取不会因此停止。", "停止全部", () -> invoke("subscriptions.stop-all", ignored -> refreshSubscriptions(body, window[0])))));
        ui.gap(body, 12); body.addView(buttons);
        LinearLayout rows = ui.column(); rows.setTag("subscription-rows"); body.addView(rows);
        window[0] = dialog("独立订阅", body);
        Runnable poll = new Runnable() {
            public void run() { if (!window[0].isShowing()) return; refreshSubscriptions(body, window[0]); handler.postDelayed(this, 1800); }
        };
        window[0].setOnDismissListener(d -> handler.removeCallbacks(poll)); poll.run();
    }

    private void refreshSubscriptions(LinearLayout body, AlertDialog window) {
        invoke("subscriptions.list", data -> {
            if (window == null || !window.isShowing()) return;
            LinearLayout rows = body.findViewWithTag("subscription-rows"); if (rows == null) return;
            rows.removeAllViews(); ui.gap(rows, 14); JSONArray items = NativeUi.array(data);
            if (items.length() == 0) rows.addView(ui.text("暂无订阅。点按添加，确认节点后开始接收值", 14, NativeUi.MUTED));
            for (int i = 0; i < items.length(); i++) {
                JSONObject item = items.optJSONObject(i); if (item == null) continue;
                LinearLayout row = ui.card(); row.addView(ui.title(status(item.optString("status")), 17));
                ui.pair(row, "端点", maskedEndpoint(item.optString("endpoint")));
                ui.pair(row, "节点", join(item.optJSONArray("node_ids"), "\n"));
                ui.pair(row, "开始", item.optString("started"));
                if (!item.optString("error").isEmpty()) row.addView(ui.text(item.optString("error"), 13, NativeUi.DANGER));
                Object last = item.opt("last_event");
                if (last instanceof JSONObject) {
                    JSONObject value = (JSONObject) last;
                    ui.pair(row, "最新值", value.optString("value", "收到通知"));
                    action(row, "查看最新通知", () -> host.show("订阅最新通知", last));
                }
                if ("running".equals(item.optString("status"))) action(row, "停止此订阅", () -> host.call(json("op", "subscriptions.stop", "subscription_id", item.optString("id")), ignored -> refreshSubscriptions(body, window)));
                rows.addView(row);
            }
        });
    }

    private void addSubscription() {
        JSONObject request = currentOr("opcua", "subscribe");
        JSONObject params = NativeUi.object(request.opt("params"));
        LinearLayout form = page();
        form.addView(ui.text("沿用当前 OPC UA 草稿的安全策略与凭据；凭据不会在此显示。新建端点时请先在请求表单配置安全连接", 13, NativeUi.MUTED));
        EditText endpoint = field(form, "服务端地址", request.optString("endpoint"), false);
        String nodes = params.has("node_ids") ? join(params.optJSONArray("node_ids"), "\n") : params.optString("node_id", "i=85");
        EditText nodeIDs = field(form, "节点 ID · 每行一个", nodes, true);
        EditText interval = number(form, "发布间隔（50–60000 毫秒）", params.optInt("interval_ms", 1000));
        EditText maxEvents = number(form, "最多通知数（1–100000）", params.optInt("max_events", 1000));
        submit("添加独立订阅", form, "预览订阅", window -> {
            JSONObject copy = NativeUi.clone(request), p = NativeUi.clone(params);
            JSONArray ids = new JSONArray(); LinkedHashSet<String> unique = new LinkedHashSet<>();
            for (String line : text(nodeIDs).split("[\\r\\n]+")) if (!line.trim().isEmpty()) unique.add(line.trim());
            if (unique.isEmpty() || unique.size() > 1000) throw new IllegalArgumentException("请输入 1–1000 个不同节点 ID");
            for (String node : unique) ids.put(node);
            p.remove("node_id"); p.put("node_ids", ids); p.put("interval_ms", integer(interval, 50, 60000)); p.put("max_events", integer(maxEvents, 1, 100000));
            copy.put("protocol", "opcua"); copy.put("action", "subscribe"); copy.put("endpoint", required(endpoint)); copy.put("params", p);
            host.call(json("op", "preview", "request", copy), result -> {
                if (!window.isShowing()) return;
                JSONObject preview = NativeUi.object(result), resolved = NativeUi.object(preview.opt("request"));
                LinearLayout scope = page(); ui.pair(scope, "端点", resolved.optString("endpoint")); ui.pair(scope, "节点", join(ids, "\n"));
                ui.pair(scope, "间隔", text(interval) + " ms"); ui.pair(scope, "上限", text(maxEvents) + " 条通知");
                scope.addView(ui.text("开始后可继续读取其他节点。关闭窗口不会停止订阅", 13, NativeUi.MUTED));
                confirmView("确认订阅", scope, "开始订阅", () -> host.call(json("op", "run", "token", preview.optString("token")), started -> { window.dismiss(); subscriptions(); }));
            });
        });
    }

    private void connections() {
        invoke("opcua.connections", data -> {
            JSONArray rows = NativeUi.array(data); LinearLayout body = page();
            body.addView(ui.text("只记录实际连接成功的端点与安全元数据，不保存账号或密码", 13, NativeUi.MUTED));
            if (rows.length() == 0) body.addView(ui.text("暂无成功连接记录", 15, NativeUi.MUTED));
            for (int i = 0; i < rows.length(); i++) {
                JSONObject row = rows.optJSONObject(i); if (row == null) continue;
                LinearLayout card = ui.card(); card.addView(ui.title(maskedEndpoint(row.optString("endpoint")), 16));
                ui.pair(card, "最近成功", row.optString("last_connected"));
                ui.pair(card, "安全策略", row.optString("security_policy", "默认")); ui.pair(card, "消息模式", row.optString("security_mode", "默认"));
                if (!row.optString("server_cert_sha256").isEmpty()) ui.pair(card, "证书指纹", row.optString("server_cert_sha256"));
                action(card, "据此创建连接草稿", () -> {
                    JSONObject params = json("node_id", row.optString("node_id", "i=85"));
                    for (String key : new String[]{"security_policy", "security_mode", "server_cert_sha256"}) if (!row.optString(key).isEmpty()) put(params, key, row.optString(key));
                    host.prepare(json("id", "opcua-history-" + System.currentTimeMillis(), "name", "从成功记录新建", "protocol", "opcua", "action", "read", "endpoint", row.optString("endpoint"), "timeout", "10s", "params", params));
                }); body.addView(card);
            }
            if (rows.length() > 0) action(body, "清除本机连接记录", () -> confirm("清除连接记录", "删除本机保存的成功连接元数据，不会修改请求集合或服务器。", "清除", () -> host.call(json("op", "opcua.connections.clear", "confirmed", true), ignored -> host.show("连接记录", "已清除本机成功连接记录"))));
            dialog("成功连接记录", body);
        });
    }

    private void identity() {
        LinearLayout form = page();
        form.addView(ui.text("生成应用私有目录中的新证书和 RSA 私钥。已有文件不会覆盖；私钥内容不会显示或自动导出", 13, NativeUi.MUTED));
        String suffix = Long.toString(System.currentTimeMillis());
        EditText cert = field(form, "新证书文件名", "opcua-client-" + suffix + ".pem", false);
        EditText key = field(form, "新私钥文件名", "opcua-client-" + suffix + "-key.pem", false);
        EditText uri = field(form, "Application URI", "urn:iotools:android:client", false);
        submit("生成 OPC UA 身份", form, "检查生成范围", window -> {
            String certName = fileName(cert), keyName = fileName(key), application = required(uri);
            if (certName.equals(keyName)) throw new IllegalArgumentException("证书和私钥需使用不同的新文件名");
            if (!application.matches("[A-Za-z][A-Za-z0-9+.-]*:.+") || application.contains("#")) throw new IllegalArgumentException("Application URI 必须是绝对 URI 且不含 #");
            confirm("生成新身份", "证书：" + certName + "\n私钥：" + keyName + "\nApplication URI：" + application + "\n\n文件仅保存在应用内，完成后可在请求表单选择。", "生成", () -> host.call(json("op", "opcua.identity", "cert_path", certName, "key_path", keyName, "application_uri", application, "confirmed", true), result -> { window.dismiss(); host.start(NativeUi.object(result)); }));
        });
    }

    private void discovery() {
        LinearLayout form = page();
        form.addView(ui.text("仅探测明确列出的 IP，或对齐的 /24–/32 网段，最多 254 个目标。端口开放不代表设备支持 Modbus", 13, NativeUi.MUTED));
        EditText targets = field(form, "IP 列表（逗号分隔）或 CIDR", "", true);
        targets.setHint("192.168.1.20,192.168.1.21");
        Spinner method = picker(form, "探测方法", new String[]{"tcp", "ping"}, new String[]{"TCP 端口", "IPv4 Ping"}, 0);
        EditText port = number(form, "TCP 端口", 502), timeout = number(form, "每目标超时（100–2000 ms）", 500), concurrency = number(form, "并发（1–32）", 8);
        submit("Modbus 网络探测", form, "展开目标预览", window -> {
            int targetPort = integer(port, 1, 65535);
            host.call(json("op", "modbus.discovery.preview", "target", required(targets), "method", selected(method), "port", targetPort, "timeout_ms", integer(timeout, 100, 2000), "concurrency", integer(concurrency, 1, 32)), result -> {
                if (!window.isShowing()) return;
                JSONObject preview = NativeUi.object(result); JSONArray ips = preview.optJSONArray("targets");
                LinearLayout scope = page(); scope.addView(ui.text(preview.optString("notice"), 14, NativeUi.DANGER));
                ui.pair(scope, "方法", preview.optString("method")); ui.pair(scope, "目标数", Integer.toString(ips == null ? 0 : ips.length()));
                ui.pair(scope, "端口", preview.optString("port")); ui.pair(scope, "超时 / 并发", preview.optString("timeout_ms") + " ms / " + preview.optString("concurrency"));
                scope.addView(ui.label("以下是本次会联系的全部地址")); scope.addView(ui.selectable(join(ips, "\n")));
                confirmView("确认网络探测范围", scope, "开始探测", () -> host.call(json("op", "modbus.discovery.run", "token", preview.optString("token"), "confirmed", true), data -> {
                    JSONObject response = NativeUi.object(data); discoveryRun = response.optString("run_id"); discoveryPort = targetPort; window.dismiss(); host.start(response);
                }));
            });
        });
    }

    private void discoveryResults() {
        LinearLayout body = page(); body.addView(ui.text("连通性结果不能证明 Modbus 协议可用。选择地址只创建草稿", 13, NativeUi.MUTED));
        EditText draftPort = number(body, "所创建草稿的 TCP 端口", discoveryPort);
        List<JSONObject> matches = new ArrayList<>();
        String latestRun = discoveryRun;
        if (latestRun.isEmpty()) for (JSONObject event : host.events()) if ("discovery".equals(event.optString("kind"))) latestRun = event.optString("run_id");
        for (JSONObject event : host.events()) if ("discovery".equals(event.optString("kind")) && latestRun.equals(event.optString("run_id"))) matches.add(NativeUi.object(event.opt("data")));
        if (matches.isEmpty()) body.addView(ui.text("本次会话没有可用探测结果，请先预览并运行探测", 14, NativeUi.MUTED));
        for (JSONObject item : matches) {
            LinearLayout card = ui.card(); card.addView(ui.title(item.optString("address"), 18)); ui.pair(card, "结果", item.optBoolean("open") ? "连通" : "未连通 · " + item.optString("error"));
            ui.pair(card, "进度", item.optString("completed") + " / " + item.optString("total"));
            if (item.optBoolean("open")) action(card, "使用此地址创建读取草稿", () -> {
                int port; try { port = integer(draftPort, 1, 65535); } catch (IllegalArgumentException invalid) { return; }
                String address = item.optString("address"); if (address.contains(":")) address = "[" + address + "]";
                host.prepare(json("id", "modbus-discovered-" + System.currentTimeMillis(), "name", "探测地址读取", "protocol", "modbus", "action", "read-holding", "endpoint", "tcp://" + address + ":" + port, "timeout", "3s", "params", json("unit", 1, "address", 0, "count", 1)));
            }); body.addView(card);
        }
        final String run = latestRun;
        if (!run.isEmpty()) action(body, "停止这次探测", () -> host.call(json("op", "cancel", "run_id", run), ignored -> host.show("网络探测", "已请求停止这次探测")));
        dialog("网络探测结果", body);
    }

    private void snapshots() {
        LinearLayout body = page(); body.addView(ui.text("快照只保存原始 u16 值。比较时强制校验端点、设备单元及寄存器空间一致", 13, NativeUi.MUTED));
        action(body, "从单次响应保存新快照", this::saveSnapshot);
        action(body, "载入快照作为基线", () -> chooseFile("选择快照文件", name -> host.call(json("op", "modbus.snapshot.load", "path", name), data -> { baseline = NativeUi.clone(NativeUi.object(data)); showSnapshot(baseline, "已载入基线"); })));
        action(body, "基线与单次响应比较", this::compareSnapshot);
        action(body, "比较两个已保存快照", () -> chooseFile("选择之前的快照", before -> host.call(json("op", "modbus.snapshot.load", "path", before), a -> chooseFile("选择之后的快照", after -> host.call(json("op", "modbus.snapshot.load", "path", after), b -> snapshotDiff(NativeUi.object(a), NativeUi.object(b)))))));
        if (baseline != null) { ui.pair(body, "当前基线", baseline.optString("endpoint") + " · 单元 " + baseline.optString("unit")); action(body, "查看当前基线", () -> showSnapshot(baseline, "当前基线")); }
        dialog("寄存器快照", body);
    }

    private void saveSnapshot() {
        LinearLayout form = page(); SnapshotInput input = new SnapshotInput(form);
        EditText path = field(form, "新快照文件名（拒绝覆盖）", "registers-" + System.currentTimeMillis() + ".json", false);
        submit("保存寄存器快照", form, "检查快照", window -> {
            JSONObject snapshot = input.read(); String name = fileName(path);
            LinearLayout preview = snapshotHeader(snapshot); ui.pair(preview, "新文件", name); ui.pair(preview, "原始值个数", Integer.toString(NativeUi.object(snapshot.opt("values")).length()));
            confirmView("确认保存新快照", preview, "保存", () -> host.call(json("op", "modbus.snapshot.save", "path", name, "snapshot", snapshot, "confirmed", true), data -> { baseline = snapshot; window.dismiss(); showSnapshot(snapshot, "已保存 · " + name); }));
        });
    }

    private void compareSnapshot() {
        if (baseline == null) { info("尚未载入基线", "请先载入或保存一份寄存器快照"); return; }
        JSONObject before = NativeUi.clone(baseline); LinearLayout form = page();
        form.addView(ui.text("基线：" + maskedEndpoint(before.optString("endpoint")) + " · 单元 " + before.optString("unit") + "\n只比较本次提供的值，不读取设备", 13, NativeUi.MUTED));
        SnapshotInput input = new SnapshotInput(form);
        submit("与基线比较", form, "比较", window -> { JSONObject after = input.read(); snapshotDiff(before, after); });
    }

    private void snapshotDiff(JSONObject before, JSONObject after) {
        host.call(json("op", "modbus.snapshot.diff", "before", before, "after", after), data -> {
            JSONArray rows = NativeUi.array(data); LinearLayout body = page();
            body.addView(ui.title(rows.length() == 0 ? "原始寄存器值一致" : rows.length() + " 个地址有变化", 20));
            body.addView(ui.text("未读取表示该次快照没有此地址，不表示设备寄存器被删除", 13, NativeUi.MUTED));
            addRows(body, rows, row -> "地址 " + row.optString("address"), row -> "之前 " + cell(row.opt("before")) + "   →   之后 " + cell(row.opt("after")), row -> host.show("寄存器差异", row));
            action(body, "导出差异 CSV", () -> { StringBuilder csv = new StringBuilder("address,before,after\n"); for (int i = 0; i < rows.length(); i++) { JSONObject row = rows.optJSONObject(i); if (row != null) csv.append(row.optString("address")).append(',').append(csvCell(row.opt("before"))).append(',').append(csvCell(row.opt("after"))).append('\n'); } host.exportText("register-diff.csv", csv.toString()); });
            dialog("快照比较结果", body);
        });
    }

    private void showSnapshot(JSONObject snapshot, String title) {
        LinearLayout body = snapshotHeader(snapshot); JSONObject values = NativeUi.object(snapshot.opt("values")); JSONArray rows = new JSONArray();
        List<String> keys = sortedAddresses(values);
        for (String address : keys) rows.put(json("address", address, "u16", values.opt(address)));
        addRows(body, rows, row -> "地址 " + row.optString("address"), row -> "u16  " + row.optString("u16"), row -> host.show("寄存器原始值", row));
        action(body, "导出快照 JSON", () -> host.exportText("register-snapshot.json", NativeUi.pretty(snapshot)));
        dialog(title, body);
    }

    private LinearLayout snapshotHeader(JSONObject snapshot) {
        LinearLayout body = page(); ui.pair(body, "端点", maskedEndpoint(snapshot.optString("endpoint"))); ui.pair(body, "设备单元", snapshot.optString("unit"));
        ui.pair(body, "空间", snapshot.optString("action").equals("read-input") ? "输入寄存器" : "保持寄存器"); ui.pair(body, "时间", snapshot.optString("time")); return body;
    }

    private void csvDiff() {
        LinearLayout form = page(); form.addView(ui.text("CSV 不包含可信设备身份，请核对端点与单元。比较仅使用本次输入的原始值", 13, NativeUi.MUTED));
        Spinner kind = picker(form, "当前寄存器空间", new String[]{"holding", "input", "coil", "discrete"}, new String[]{"保持寄存器", "输入寄存器", "线圈", "离散输入"}, 0);
        WordInput words = new WordInput(form);
        EditText csv = field(form, "CSV 内容（type,address,u16…）", "", true); csv.setHint("type,address,u16\nholding,0,123");
        action(form, "从应用文件载入 CSV", () -> chooseFile("选择 CSV 文件", name -> host.call(json("op", "file.read", "path", name), data -> { JSONObject value = NativeUi.object(data); if (!value.has("text")) info("无法读取文本", "此文件不是 UTF-8 CSV"); else csv.setText(value.optString("text")); })));
        CheckBox hex = check(form, "CSV 的地址列按十六进制解释", false);
        submit("CSV 与当前值比较", form, "比较 CSV", window -> host.call(json("op", "modbus.csv.diff", "source", required(csv), "kind", selected(kind), "hex_address", hex.isChecked(), "words", words.read()), this::showCsvDiff));
    }

    private void showCsvDiff(Object data) {
        JSONArray rows = NativeUi.array(data); LinearLayout body = page(); body.addView(ui.text("未读取不等于删除；相同地址的不同寄存器空间分别比较", 13, NativeUi.MUTED));
        addRows(body, rows, row -> { JSONObject cell = NativeUi.object(row.opt("Cell")); return cell.optString("Type") + " · 地址 " + cell.optString("Address"); }, row -> "CSV " + cell(row.opt("Before")) + "   →   当前 " + cell(row.opt("After")), row -> host.show("CSV 单元详情", row));
        action(body, "导出比较 CSV", () -> { StringBuilder csv = new StringBuilder("type,address,before,after,time\n"); for (int i = 0; i < rows.length(); i++) { JSONObject row = rows.optJSONObject(i); if (row == null) continue; JSONObject c = NativeUi.object(row.opt("Cell")); csv.append(csvCell(c.opt("Type"))).append(',').append(csvCell(c.opt("Address"))).append(',').append(csvCell(row.opt("Before"))).append(',').append(csvCell(row.opt("After"))).append(',').append(csvCell(row.opt("Time"))).append('\n'); } host.exportText("csv-diff.csv", csv.toString()); });
        dialog("CSV 比较结果", body);
    }

    private void interpret() {
        JSONObject request; try { request = requireProtocol("modbus"); } catch (Exception e) { error(e); return; }
        LinearLayout form = page(); form.addView(ui.text("使用当前草稿的标签、自定义规则与字节顺序。只接受同一响应中的值，不合并不同时间的数据", 13, NativeUi.MUTED));
        WordInput words = new WordInput(form);
        submit("解释寄存器响应", form, "本机解码", window -> host.call(json("op", "modbus.interpret", "request", request, "words", words.read()), result -> {
            JSONArray rows = NativeUi.array(result); LinearLayout body = page();
            addRows(body, rows, row -> "地址 " + row.optString("address") + "  " + row.optString("label"), row -> "u16 " + row.optString("u16") + "   i16 " + row.optString("i16") + "   自定义 " + row.optString("custom", "—"), row -> host.show("寄存器全部解码", row));
            action(body, "导出解释结果", () -> host.exportText("registers-interpreted.json", NativeUi.pretty(result))); dialog("单次响应解释", body);
        }));
    }

    private void importRegisters() {
        JSONObject request; try { request = requireProtocol("modbus"); } catch (Exception e) { error(e); return; }
        LinearLayout form = page();
        form.addView(ui.text("导入 MTUI 寄存器定义，仅替换选定空间的标签、固定行和自定义规则。确认后先形成草稿，请检查后保存", 13, NativeUi.MUTED));
        Spinner kind = picker(form, "导入空间", new String[]{"read-holding", "read-input", "read-coils", "read-discrete"}, new String[]{"保持寄存器", "输入寄存器", "线圈", "离散输入"}, 0);
        EditText source = field(form, "粘贴 MTUI 配置或寄存器 JSON", "", true);
        source.setHint("{\"holdings\":[{\"address\":0,\"label\":\"温度\",\"pinned\":true}]}");
        action(form, "从应用文件载入", () -> chooseFile("选择 MTUI JSON 文件", name -> host.call(json("op", "file.read", "path", name), data -> { JSONObject value = NativeUi.object(data); if (!value.has("text")) info("无法导入", "此文件不是 UTF-8 文本"); else source.setText(value.optString("text")); })));
        submit("导入寄存器定义", form, "预览导入", window -> {
            String action = selected(kind);
            host.call(json("op", "modbus.registers.import", "kind", action, "source", required(source)), data -> {
                if (!window.isShowing()) return;
                JSONObject imported = NativeUi.object(data), labels = NativeUi.object(imported.opt("labels")); JSONArray pins = NativeUi.array(imported.opt("pins")), rules = NativeUi.array(imported.opt("rules"));
                LinearLayout preview = page(); ui.pair(preview, "寄存器空间", action); ui.pair(preview, "标签数", Integer.toString(labels.length())); ui.pair(preview, "固定行数", Integer.toString(pins.length())); ui.pair(preview, "自定义规则", Integer.toString(rules.length()));
                JSONArray labelRows = new JSONArray(); for (String address : sortedAddresses(labels)) labelRows.put(json("address", address, "label", labels.optString(address)));
                if (labelRows.length() > 0) addRows(preview, labelRows, row -> "地址 " + row.optString("address"), row -> row.optString("label"), row -> host.show("寄存器标签", row));
                if (pins.length() > 0) ui.pair(preview, "固定地址", join(pins, ", ")); if (rules.length() > 0) action(preview, "检查导入规则", () -> host.show("寄存器规则", rules));
                confirmView("确认导入到草稿", preview, "应用到新草稿", () -> {
                    JSONObject copy = NativeUi.clone(request), params = NativeUi.clone(NativeUi.object(copy.opt("params")));
                    for (String key : new String[]{"pins", "labels", "rules"}) put(params, key, imported.opt(key));
                    put(copy, "params", params); put(copy, "action", action); put(copy, "id", "modbus-import-" + System.currentTimeMillis()); put(copy, "name", "导入寄存器定义"); window.dismiss(); host.prepare(copy);
                });
            });
        });
    }

    private void controller() {
        LinearLayout form = page();
        form.addView(ui.text("只监听本机 127.0.0.1。其他应用可访问此本机接口；写入默认关闭，启用后必须限定单元、空间及地址范围。切到后台会停止服务", 13, NativeUi.MUTED));
        EditText port = number(form, "本机端口（1–65535）", 8080);
        CheckBox writable = check(form, "允许下面限定范围内的设备写入", false);
        JSONObject draft = currentOr("modbus", "read-holding"), params = NativeUi.object(draft.opt("params"));
        EditText unit = number(form, "设备单元（与当前请求一致）", params.optInt("unit", 1));
        Spinner space = picker(form, "允许写入的空间", new String[]{"holding", "coil"}, new String[]{"保持寄存器", "线圈"}, 0);
        EditText address = number(form, "允许写入的起始地址", params.optInt("address", 0)), count = number(form, "允许写入的地址数量（1–1968）", 1);
        View[] scoped = {unit, space, address, count}; for (View view : scoped) view.setEnabled(false);
        writable.setOnCheckedChangeListener((button, checked) -> { for (View view : scoped) view.setEnabled(checked); });
        if (!controllerRun.isEmpty()) {
            final String run = controllerRun;
            action(form, "停止这个本机接口", () -> host.call(json("op", "cancel", "run_id", run), data -> { controllerRun = ""; info("本机接口", "已请求停止本机控制服务"); }));
        }
        submit("本机 Modbus 控制接口", form, "检查服务范围", window -> {
            JSONObject request = requireProtocol("modbus"); String listen = "127.0.0.1:" + integer(port, 1, 65535);
            // The service chooses its read/write action per HTTP call; preview its safe base connection.
            put(request, "action", "read-holding"); JSONObject connectionParams = NativeUi.clone(NativeUi.object(request.opt("params")));
            put(connectionParams, "address", 0); put(connectionParams, "count", 1); put(request, "params", connectionParams);
            JSONObject scope = null;
            if (writable.isChecked()) {
                int start = integer(address, 0, 65535), length = integer(count, 1, 1968);
                if (start + length > 65536) throw new IllegalArgumentException("写入范围不能越过地址 65535");
                scope = json("Unit", integer(unit, 1, 247), "Address", start, "Count", length, "Type", selected(space));
            }
            final JSONObject writeScope = scope;
            host.call(json("op", "preview", "request", request), result -> {
                if (!window.isShowing()) return;
                JSONObject preview = NativeUi.object(result), resolved = NativeUi.object(preview.opt("request"));
                if (writeScope != null && writeScope.optInt("Unit") != NativeUi.object(resolved.opt("params")).optInt("unit", -1)) { info("单元不匹配", "写入范围的单元必须与当前请求解析后的设备单元一致"); return; }
                LinearLayout review = page(); ui.pair(review, "监听", listen); ui.pair(review, "设备端点", resolved.optString("endpoint"));
                ui.pair(review, "设备单元", NativeUi.object(resolved.opt("params")).optString("unit"));
                if (writeScope == null) ui.pair(review, "权限", "只读，不提供写入");
                else { ui.pair(review, "允许写入", writeScope.optString("Type")); ui.pair(review, "设备单元", writeScope.optString("Unit")); ui.pair(review, "地址范围", writeScope.optInt("Address") + "–" + (writeScope.optInt("Address") + writeScope.optInt("Count") - 1)); review.addView(ui.text("本机其他应用可在此范围内更改设备状态。确认后服务持续运行，直到停止或离开前台", 14, NativeUi.DANGER)); }
                confirmView("确认启动控制接口", review, writeScope == null ? "启动只读接口" : "允许限定写入并启动", () -> {
                    JSONObject command = json("op", "modbus.controller.start", "request", request, "listen", listen, "confirmed", true); if (writeScope != null) put(command, "scope", writeScope);
                    host.call(command, data -> { JSONObject response = NativeUi.object(data); controllerRun = response.optString("run_id"); window.dismiss(); host.start(response); });
                });
            });
        });
    }

    private void stats(Object data) {
        JSONObject result = NativeUi.object(data); LinearLayout body = page();
        ui.pair(body, "实际操作", result.optString("operations", "0")); ui.pair(body, "成功", result.optString("success", "0")); ui.pair(body, "失败", result.optString("failure", "0")); ui.pair(body, "总耗时", result.optString("duration_ms", "0") + " ms");
        JSONObject last = result.optJSONObject("last");
        if (last != null) { ui.pair(body, "最近动作", last.optString("action")); ui.pair(body, "单元 / 地址", last.optString("unit") + " / " + last.optString("address")); ui.pair(body, "地址数量", last.optString("count")); ui.pair(body, "耗时", last.optString("duration_ms") + " ms"); ui.pair(body, "结果", last.optBoolean("cancelled") ? "已取消" : last.optBoolean("success") ? "成功" : last.optString("error_class")); }
        action(body, "导出会话统计", () -> host.exportText("modbus-session.json", NativeUi.pretty(data))); dialog("Modbus 会话统计", body);
    }

    void files() {
        invoke("files.list", data -> {
            JSONArray rows = NativeUi.array(data); LinearLayout body = page();
            body.addView(ui.text("应用私有文件 · 点按查看可用操作。私钥只显示文件名，不显示内容", 13, NativeUi.MUTED));
            addRows(body, rows, row -> row.optString("name"), row -> row.optString("bytes") + " 字节 · " + row.optString("modified"), this::fileActions);
            dialog("应用文件", body);
        });
    }

    private void fileActions(JSONObject file) {
        String name = file.optString("name"); LinearLayout body = page();
        ui.pair(body, "文件名", name); ui.pair(body, "大小", file.optString("bytes") + " 字节"); ui.pair(body, "修改时间", file.optString("modified"));
        if (isPrivateKey(name)) body.addView(ui.text("私钥内容已隐藏。请通过请求表单中的证书/私钥文件字段引用此文件", 14, NativeUi.MUTED));
        else action(body, "明确打开文件内容", () -> confirm("打开文件内容", "文件可能包含凭据或响应数据。确认后仅在本机查看。\n" + name, "查看", () -> host.call(json("op", "file.read", "path", name), data -> { JSONObject value = NativeUi.object(data); host.show(name, value.has("text") ? value.opt("text") : json("字节数", value.opt("bytes"), "说明", "二进制文件；使用支持此格式的工作流读取")); })));
        String lower = name.toLowerCase(Locale.ROOT);
        if (lower.endsWith("yaml") || lower.endsWith("yml") || lower.endsWith("json")) action(body, "预览切换到此集合", () -> previewSwitch(name));
        if (lower.endsWith("json")) action(body, "载入为寄存器快照", () -> host.call(json("op", "modbus.snapshot.load", "path", name), data -> { baseline = NativeUi.clone(NativeUi.object(data)); showSnapshot(baseline, "已载入基线"); }));
        dialog(name, body);
    }

    private void previewSwitch(String name) {
        host.call(json("op", "config.switch", "path", name), data -> {
            JSONObject result = NativeUi.object(data), collection = NativeUi.object(result.opt("collection")); JSONArray requests = collection.optJSONArray("requests");
            LinearLayout body = page(); ui.pair(body, "集合文件", name); ui.pair(body, "请求数", Integer.toString(requests == null ? 0 : requests.length())); ui.pair(body, "默认环境", collection.optString("default_profile", "默认"));
            body.addView(ui.text("切换会关闭独立订阅，并丢弃当前未保存的表单和 YAML 草稿。请先保存要保留的修改。切换后不会自动执行请求", 14, NativeUi.DANGER));
            if (requests != null) for (int i = 0; i < Math.min(requests.length(), 30); i++) { JSONObject request = requests.optJSONObject(i); if (request != null) ui.pair(body, request.optString("protocol").toUpperCase(Locale.ROOT), request.optString("name", request.optString("id"))); }
            confirmView("确认切换集合", body, "丢弃草稿并切换", () -> host.call(json("op", "config.switch", "path", name, "token", result.optString("token"), "confirmed", true), changed -> host.stateChanged(NativeUi.object(changed))));
        });
    }

    private void chooseFile(String title, java.util.function.Consumer<String> choice) {
        invoke("files.list", data -> {
            JSONArray rows = NativeUi.array(data); LinearLayout body = page(); final AlertDialog[] window = new AlertDialog[1];
            addRows(body, rows, row -> row.optString("name"), row -> row.optString("bytes") + " 字节", row -> { window[0].dismiss(); choice.accept(row.optString("name")); });
            window[0] = dialog(title, body);
        });
    }

    private final class SnapshotInput {
        final EditText endpoint, unit;
        final Spinner action;
        final WordInput words;
        SnapshotInput(LinearLayout form) {
            JSONObject request = currentOr("modbus", "read-holding"), params = NativeUi.object(request.opt("params"));
            form.addView(ui.text("核对数据来源后再保存。原始值必须来自同一设备单元、同一空间和同一次响应", 13, NativeUi.MUTED));
            endpoint = field(form, "数据来源端点", request.optString("endpoint"), false); unit = number(form, "设备单元（1–247）", params.optInt("unit", 1));
            action = picker(form, "寄存器空间", new String[]{"read-holding", "read-input"}, new String[]{"保持寄存器", "输入寄存器"}, request.optString("action").equals("read-input") ? 1 : 0);
            words = new WordInput(form);
        }
        JSONObject read() throws Exception { return json("version", 1, "endpoint", required(endpoint), "unit", integer(unit, 1, 247), "action", selected(action), "time", Instant.now().toString(), "values", words.read()); }
    }

    private final class WordInput {
        final EditText input;
        WordInput(LinearLayout form) {
            input = field(form, "单次响应 · 每行 地址 = u16（0–65535）", "", true); input.setHint("0 = 123\n1 = 456");
            input.setMinLines(4);
            action(form, "填入最新的一次寄存器响应", () -> {
                List<JSONObject> events = host.events();
                for (int i = events.size() - 1; i >= 0; i--) {
                    JSONObject event = events.get(i); if (!"registers".equals(event.optString("kind"))) continue;
                    JSONArray rows = event.optJSONArray("data");
                    if (rows == null) {
                        String resultID = event.optString("result_id", NativeUi.object(event.opt("data")).optString("result_id"));
                        if (!resultID.isEmpty()) host.call(json("op", "result.get", "result_id", resultID), result -> fill(NativeUi.object(result).optJSONArray("data")));
                        else info("无法填入", "最新响应已被截断，无法恢复完整原始值。请缩小读取范围后重试");
                        return;
                    }
                    fill(rows); return;
                }
                info("没有寄存器响应", "请先执行一次寄存器读取，或在上方手动填写原始值");
            });
        }
        void fill(JSONArray rows) {
            if (rows == null) { info("无法填入", "结果缓存中没有可用寄存器数组，请重新读取"); return; }
            if (rows.length() > 2000) { info("响应过大", "单次解释最多 2000 个寄存器，请缩小读取范围"); return; }
            StringBuilder content = new StringBuilder();
            for (int n = 0; n < rows.length(); n++) { JSONObject row = rows.optJSONObject(n); if (row != null && row.has("address") && row.has("u16")) content.append(row.optString("address")).append(" = ").append(row.optString("u16")).append('\n'); }
            input.setText(content.toString());
        }
        JSONObject read() throws Exception {
            JSONObject words = new JSONObject(); String[] lines = required(input).split("[\\r\\n]+");
            if (lines.length > 2000) throw new IllegalArgumentException("单次响应最多 2000 个寄存器");
            for (int i = 0; i < lines.length; i++) {
                String line = lines[i].trim(); if (line.isEmpty()) continue;
                String[] pair = line.split("\\s*[=:：]\\s*", -1);
                if (pair.length != 2) throw new IllegalArgumentException("第 " + (i + 1) + " 行请输入 地址 = u16");
                int address = parseInt(pair[0].trim(), 0, 65535, "地址"), value = parseInt(pair[1].trim(), 0, 65535, "u16"); String key = Integer.toString(address);
                if (words.has(key)) throw new IllegalArgumentException("重复地址：" + address); words.put(key, value);
            }
            if (words.length() == 0) throw new IllegalArgumentException("请至少输入一个寄存器值"); return words;
        }
    }

    private LinearLayout section(LinearLayout parent, String title, String detail) { LinearLayout card = ui.card(); card.addView(ui.title(title, 19)); card.addView(ui.text(detail, 13, NativeUi.MUTED)); ui.gap(card, 10); parent.addView(card); return card; }
    private LinearLayout page() { LinearLayout page = ui.column(); page.setPadding(ui.dp(18), ui.dp(8), ui.dp(18), ui.dp(20)); return page; }
    private void action(LinearLayout parent, String text, Runnable action) { ui.gap(parent, 8); parent.addView(ui.button(text, false, action)); }
    private EditText field(LinearLayout parent, String label, String value, boolean multiline) { parent.addView(ui.label(label)); EditText field = ui.input(label, value, multiline); field.setContentDescription(label); field.setSaveEnabled(false); parent.addView(field); return field; }
    private EditText number(LinearLayout parent, String label, int value) { EditText input = field(parent, label, Integer.toString(value), false); input.setInputType(InputType.TYPE_CLASS_NUMBER); return input; }
    private CheckBox check(LinearLayout parent, String label, boolean checked) { CheckBox box = new CheckBox(context); box.setText(label); box.setTextColor(NativeUi.INK); box.setMinHeight(ui.dp(48)); box.setChecked(checked); parent.addView(box); return box; }
    private Spinner picker(LinearLayout parent, String label, String[] values, String[] labels, int selection) {
        parent.addView(ui.label(label)); Spinner spinner = new Spinner(context); spinner.setMinimumHeight(ui.dp(48)); spinner.setBackground(ui.outline(NativeUi.BG, 10)); spinner.setContentDescription(label);
        ArrayAdapter<String> adapter = new ArrayAdapter<String>(context, android.R.layout.simple_spinner_item, labels) {
            @Override public View getView(int position, View convert, ViewGroup parent) { TextView view = (TextView) super.getView(position, convert, parent); view.setTextColor(NativeUi.INK); view.setMinHeight(ui.dp(48)); view.setGravity(android.view.Gravity.CENTER_VERTICAL); return view; }
            @Override public View getDropDownView(int position, View convert, ViewGroup parent) { TextView view = (TextView) super.getDropDownView(position, convert, parent); view.setTextColor(NativeUi.INK); view.setBackgroundColor(NativeUi.CARD); view.setMinHeight(ui.dp(48)); return view; }
        };
        adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item); spinner.setAdapter(adapter); spinner.setTag(values); spinner.setSelection(selection); parent.addView(spinner); return spinner;
    }
    private String selected(Spinner spinner) { return ((String[]) spinner.getTag())[spinner.getSelectedItemPosition()]; }
    private AlertDialog dialog(String title, LinearLayout body) { AlertDialog dialog = new AlertDialog.Builder(context).setTitle(title).setView(ui.scroll(body)).setPositiveButton("关闭", null).create(); style(dialog); return dialog; }
    private void style(AlertDialog dialog) { dialog.show(); if (dialog.getWindow() != null) dialog.getWindow().setBackgroundDrawable(ui.shape(NativeUi.CARD, 18)); for (int which : new int[]{AlertDialog.BUTTON_POSITIVE, AlertDialog.BUTTON_NEGATIVE, AlertDialog.BUTTON_NEUTRAL}) { Button button = dialog.getButton(which); if (button != null) { button.setTextColor(NativeUi.ACCENT); button.setMinHeight(ui.dp(48)); } } }
    private interface FormAction { void run(AlertDialog window) throws Exception; }
    private void submit(String title, LinearLayout body, String positive, FormAction action) {
        TextView error = ui.text("", 13, NativeUi.DANGER); error.setVisibility(View.GONE); body.addView(error);
        AlertDialog dialog = new AlertDialog.Builder(context).setTitle(title).setView(ui.scroll(body)).setNegativeButton("取消", null).setPositiveButton(positive, null).create(); style(dialog);
        dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(view -> { try { error.setVisibility(View.GONE); action.run(dialog); } catch (Exception e) { error.setText(e.getMessage() == null ? "请检查输入" : e.getMessage()); error.setVisibility(View.VISIBLE); } });
    }
    private void confirm(String title, String detail, String positive, Runnable action) { LinearLayout body = page(); body.addView(ui.text(detail, 15, NativeUi.INK)); confirmView(title, body, positive, action); }
    private void confirmView(String title, LinearLayout body, String positive, Runnable action) { AlertDialog dialog = new AlertDialog.Builder(context).setTitle(title).setView(ui.scroll(body)).setNegativeButton("取消", null).setPositiveButton(positive, (d, w) -> action.run()).create(); style(dialog); dialog.getButton(AlertDialog.BUTTON_NEGATIVE).requestFocus(); }
    private void info(String title, String message) { LinearLayout body = page(); body.addView(ui.text(message, 15, NativeUi.INK)); dialog(title, body); }
    private void error(Exception error) { info("无法继续", error.getMessage() == null ? "请检查输入" : error.getMessage()); }
    private void invoke(String op, Callback success) { host.call(json("op", op), success); }
    private String required(EditText input) { String value = text(input); if (value.isEmpty()) { input.setError("此项必填"); throw new IllegalArgumentException("请填写所有必填项"); } return value; }
    private String text(EditText input) { return input.getText().toString().trim(); }
    private int integer(EditText input, int min, int max) { try { return parseInt(text(input), min, max, input.getContentDescription().toString()); } catch (IllegalArgumentException e) { input.setError(e.getMessage()); throw e; } }
    private int parseInt(String text, int min, int max, String label) { try { if (!text.matches("[0-9]+")) throw new NumberFormatException(); int value = Integer.parseInt(text); if (value < min || value > max) throw new NumberFormatException(); return value; } catch (NumberFormatException e) { throw new IllegalArgumentException(label + " 必须是 " + min + "–" + max + " 的整数"); } }
    private String fileName(EditText input) { String name = required(input); if (name.equals(".") || name.equals("..") || name.contains("/") || name.contains("\\") || name.length() > 180) throw new IllegalArgumentException("请输入应用内的新文件名，不包含目录"); return name; }
    private JSONObject requireProtocol(String protocol) throws JSONException { JSONObject request = NativeUi.clone(host.request()); if (!protocol.equals(request.optString("protocol"))) throw new IllegalArgumentException("请先选择 " + protocol.toUpperCase(Locale.ROOT) + " 请求"); return request; }
    private JSONObject currentOr(String protocol, String action) { try { JSONObject request = requireProtocol(protocol); return request; } catch (Exception ignored) { return json("id", protocol + "-" + System.currentTimeMillis(), "name", "新建 " + protocol, "protocol", protocol, "action", action, "endpoint", "", "timeout", "10s", "params", new JSONObject()); } }
    private static JSONObject json(Object... values) { return NativeUi.json(values); }
    private static void put(JSONObject object, String key, Object value) { try { object.put(key, value); } catch (JSONException e) { throw new IllegalArgumentException(e); } }
    private String join(JSONArray values, String delimiter) { if (values == null) return ""; StringBuilder out = new StringBuilder(); for (int i = 0; i < values.length(); i++) { if (i > 0) out.append(delimiter); out.append(values.optString(i)); } return out.toString(); }
    private String status(String value) { switch (value) { case "running": return "正在订阅"; case "completed": return "已完成"; case "cancelled": return "已停止"; case "failed": return "失败"; default: return value; } }
    private String cell(Object value) { return value == null || value == JSONObject.NULL ? "未读取" : String.valueOf(value); }
    private String csvCell(Object value) { if (value == null || value == JSONObject.NULL) return ""; return "\"" + String.valueOf(value).replace("\"", "\"\"") + "\""; }
    private List<String> sortedAddresses(JSONObject values) { List<String> keys = new ArrayList<>(); Iterator<String> iterator = values.keys(); while (iterator.hasNext()) keys.add(iterator.next()); Collections.sort(keys, (a, b) -> Integer.compare(Integer.parseInt(a), Integer.parseInt(b))); return keys; }
    private boolean isPrivateKey(String name) { String lower = name.toLowerCase(Locale.ROOT); return lower.contains("key") || lower.endsWith(".p12") || lower.endsWith(".pfx") || lower.endsWith(".keystore"); }
    private String maskedEndpoint(String endpoint) { return endpoint.replaceAll("(?<=://)[^/@]+@", "••••@").replaceAll("(?i)([?&](?:token|password|key|secret|access_token)=)[^&#]*", "$1••••"); }
    private interface RowText { String text(JSONObject row); }
    private void addRows(LinearLayout parent, JSONArray rows, RowText title, RowText subtitle, java.util.function.Consumer<JSONObject> click) {
        if (rows.length() == 0) { ui.gap(parent, 12); parent.addView(ui.text("没有数据", 14, NativeUi.MUTED)); return; }
        ListView list = new ListView(context); list.setDividerHeight(ui.dp(1)); list.setBackgroundColor(NativeUi.CARD); list.setNestedScrollingEnabled(true);
        list.setAdapter(new BaseAdapter() {
            public int getCount() { return rows.length(); }
            public Object getItem(int position) { return rows.optJSONObject(position); }
            public long getItemId(int position) { return position; }
            public View getView(int position, View convert, ViewGroup group) {
                LinearLayout row = convert instanceof LinearLayout ? (LinearLayout) convert : ui.column(); row.removeAllViews(); row.setPadding(ui.dp(10), ui.dp(12), ui.dp(10), ui.dp(12)); row.setMinimumHeight(ui.dp(64));
                JSONObject value = NativeUi.object(rows.opt(position)); row.addView(ui.title(title.text(value), 15)); row.addView(ui.text(subtitle.text(value), 12, NativeUi.MUTED)); return row;
            }
        });
        list.setOnItemClickListener((p, view, position, id) -> click.accept(NativeUi.object(rows.opt(position))));
        ui.gap(parent, 12); parent.addView(list, new LinearLayout.LayoutParams(-1, ui.dp(Math.min(400, Math.max(72, rows.length() * 72)))));
    }
}
