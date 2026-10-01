package io.github.liveyum.iotools;

import static org.junit.Assert.*;

import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.ApplicationInfo;
import android.os.Build;
import android.os.Bundle;
import android.os.SystemClock;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.filters.LargeTest;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.uiautomator.By;
import androidx.test.uiautomator.Direction;
import androidx.test.uiautomator.UiDevice;
import androidx.test.uiautomator.UiObject2;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.net.Socket;
import java.net.URI;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.StandardCopyOption;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;
import java.util.zip.ZipFile;

/**
 * 普通发布 APK 的黑盒验收。实际协议请求只能由可见 Flutter 控件触发。
 * NativeRuntime 仅用于启动前读取构建元数据，以及关闭/恢复测试环境。
 * 不调用 run/preview/dispatch，不启动 integration_test Dart 入口。
 *
 * 运行器须传入 apk_sha256、build_sha，并 adb reverse 48410..48416。
 * 证据写入目标应用自有 externalFiles/aot-evidence，不申请用户存储权限。
 */
@RunWith(AndroidJUnit4.class)
@LargeTest
public final class AotAcceptanceTest {
    private static final String PACKAGE = "io.github.liveyum.iotools";
    private static final long UI_TIMEOUT_MS = 30_000L;
    private static final String NETWORK_METRICS = "http://127.0.0.1:48416/metrics";
    private static final String OPC_METRICS = "http://127.0.0.1:48411/metrics";
    private static final String KAFKA_METRICS = "http://127.0.0.1:48413/metrics";
    private UiDevice device;
    private File evidence;
    private final JSONObject report = new JSONObject();
    private final JSONObject protocols = new JSONObject();
    private String stage = "初始化", suffix;
    private boolean fixtureUiVisible;
    private int artifactIndex;

    @Test(timeout = 480_000L)
    public void normalReleaseFlutterEntryRunsAllProtocolsThroughVisibleControls() throws Throwable {
        Context target = InstrumentationRegistry.getInstrumentation().getTargetContext();
        device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        // Instrumentation runs under the target UID. Its test-package context
        // cannot own an external-files directory under Android scoped storage.
        File external = target.getExternalFilesDir(null);
        assertNotNull("目标应用外部私有证据目录不可用，无法完成验收证据采集", external);
        evidence = new File(external, "aot-evidence");
        assertTrue("无法创建证据目录", evidence.isDirectory() || evidence.mkdirs());
        report.put("schema", 1).put("status", "running").put("protocols", protocols);
        report.put("scope", "合成回环夹具；不是物理 ARM64 或工业设备验收");
        report.put("started_utc_ms", System.currentTimeMillis());
        suffix = Long.toHexString(SystemClock.elapsedRealtimeNanos());
        report.put("test_run_id", suffix);
        SharedPreferences preferences = target.getSharedPreferences("flutter-settings", Context.MODE_PRIVATE);
        Map<String, ?> originalPreferences = new HashMap<>(preferences.getAll());
        File privateRoot = target.getFilesDir().getCanonicalFile();
        File rootConfig = new File(privateRoot, "iotools.yaml");
        boolean rootExisted = rootConfig.exists();
        byte[] originalRoot = rootExisted ? boundedRead(rootConfig, 4L << 20) : null;
        File temporary = null, damaged = null;
        ActivityScenario<MainActivity> scenario = null;
        Throwable primary = null;
        try {
            stage = "核对安装文件与构建标识";
            Bundle arguments = InstrumentationRegistry.getArguments();
            String expectedHash = arguments.getString("apk_sha256", "").toLowerCase();
            String expectedBuild = arguments.getString("build_sha", "");
            assertTrue("必须传入完整 APK SHA256", expectedHash.matches("[0-9a-f]{64}"));
            assertTrue("必须传入完整构建 SHA", expectedBuild.matches("[0-9a-fA-F]{40}"));
            assertEquals("必须验收正式目标包", PACKAGE, target.getPackageName());
            ApplicationInfo installed = target.getApplicationInfo();
            String installedHash = sha256(new File(installed.sourceDir));
            assertEquals("安装的 APK 必须与待交付的同一文件完全一致", expectedHash, installedHash);
            assertEquals("发布目标不能带可调试标志", 0, installed.flags & ApplicationInfo.FLAG_DEBUGGABLE);
            assertTrue("验收对象必须是单一双 ABI APK", installed.splitSourceDirs == null || installed.splitSourceDirs.length == 0);
            try (ZipFile apk = new ZipFile(installed.sourceDir)) {
                assertNotNull("发布包缺少 ARM64 AOT 库", apk.getEntry("lib/arm64-v8a/libapp.so"));
                assertNotNull("同一发布包缺少 x86_64 AOT 库", apk.getEntry("lib/x86_64/libapp.so"));
                assertNull("发布包不能包含调试 kernel_blob", apk.getEntry("assets/flutter_assets/kernel_blob.bin"));
            }
            JSONObject opened = nativeData(NativeRuntime.open(rootConfig.getCanonicalPath(), expectedBuild, false, false));
            assertEquals("Go 内核版本必须等于本次构建 SHA", expectedBuild, opened.getString("version"));
            NativeRuntime.close();
            report.put("apk_sha256", installedHash).put("expected_apk_sha256", expectedHash);
            report.put("build_sha", expectedBuild).put("go_version", opened.getString("version"));
            report.put("target_package", PACKAGE).put("activity", MainActivity.class.getName());
            report.put("debuggable", false).put("normal_entry", "普通 MainActivity；安装文件哈希绑定运行器已静态检查的 lib/main.dart 入口");
            report.put("device", new JSONObject().put("brand", Build.BRAND).put("model", Build.MODEL)
                .put("device", Build.DEVICE).put("sdk", Build.VERSION.SDK_INT)
                .put("supported_abis", new JSONArray(Build.SUPPORTED_ABIS))
                .put("process_64_bit", android.os.Process.is64Bit()));
            writeReport();

            stage = "创建临时私有集合";
            temporary = new File(privateRoot, "aot-acceptance-" + suffix + ".yaml");
            assertTrue("临时集合必须新建且不得覆盖", temporary.createNewFile());
            try (FileOutputStream output = new FileOutputStream(temporary)) {
                output.write(collection().toString().getBytes(StandardCharsets.UTF_8));
                output.getFD().sync();
            }
            damaged = new File(privateRoot, "aot-damaged-" + suffix + ".yaml");
            assertTrue("损坏夹具必须新建且不得覆盖", damaged.createNewFile());
            byte[] damagedBytes = "version: 1\nrequests: [\n".getBytes(StandardCharsets.UTF_8);
            try (FileOutputStream output = new FileOutputStream(damaged)) {
                output.write(damagedBytes);
                output.getFD().sync();
            }
            assertTrue("测试设置保存失败", preferences.edit().putString("theme", "dark")
                .putBoolean("readOnly", false).putBoolean("history", false)
                .putString("collection", damaged.getName()).commit());
            JSONObject beforeStartup = metrics(NETWORK_METRICS);
            JSONObject kafkaStartup = metrics(KAFKA_METRICS), opcStartup = metrics(OPC_METRICS);
            Intent intent = new Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
                .setComponent(new ComponentName(target, MainActivity.class))
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TASK);
            scenario = ActivityScenario.launch(intent);
            stage = "普通 Flutter 冷启动损坏配置恢复";
            requireText("无法打开配置", UI_TIMEOUT_MS);
            fixtureUiVisible = true;
            capture("损坏配置_可见恢复入口");
            clickLabel("选择私有集合"); reveal(temporary.getName(), true);
            requireText("打开这个集合？", UI_TIMEOUT_MS); clickLabel("取消");
            requireText("无法打开配置", UI_TIMEOUT_MS);
            assertEquals("取消恢复不得更改集合偏好", damaged.getName(), preferences.getString("collection", ""));
            assertArrayEquals("取消恢复不得修改损坏原文件", damagedBytes, boundedRead(damaged, 4096));
            report.put("startup_recovery_cancel_preserved_original", true);
            clickLabel("选择私有集合"); reveal(temporary.getName(), true);
            requireText("打开这个集合？", UI_TIMEOUT_MS); clickLabel("校验并打开");
            requireText("请求工作台", UI_TIMEOUT_MS);
            assertEquals("校验成功后必须记住所选集合", temporary.getName(), preferences.getString("collection", ""));
            assertArrayEquals("成功恢复也不得修改损坏原文件", damagedBytes, boundedRead(damaged, 4096));
            report.put("startup_recovery_opened_valid_collection", true);
            report.put("startup_recovery_damaged_file_unchanged", true);
            stage = "普通 Flutter 入口启动";
            reveal(name("HTTP GET"), false);
            fixtureUiVisible = true;
            JSONObject afterStartup = metrics(NETWORK_METRICS);
            equalCounters(beforeStartup, afterStartup, "启动不可自动联网", "http_posts", "modbus_reads", "modbus_writes", "mqtt_packets_received");
            equalCounters(kafkaStartup, metrics(KAFKA_METRICS), "启动不可消费 Kafka", "total_requests", "produce_requests");
            equalCounters(opcStartup, metrics(OPC_METRICS), "启动不可浏览 OPC", "browses", "reads", "writes", "calls");
            capture("启动_普通Flutter入口");

            verifyHttpGet();
            verifyHttpPostCancelAndConfirm();
            verifyMqtt();
            verifyKafka();
            verifyModbus();
            verifyOpc();
            report.put("status", "passed").put("finished_utc_ms", System.currentTimeMillis());
            capture("全部协议通过");
        } catch (Throwable failure) {
            primary = failure;
            report.put("status", "failed").put("failed_stage", stage)
                .put("failure_type", failure.getClass().getName());
            // 不记录请求源、凭据、完整异常或私有配置内容。
            if (scenario != null || fixtureUiVisible) {
                try { capture("失败现场"); } catch (Throwable ignored) { report.put("failure_capture_unavailable", true); }
            }
            throw failure;
        } finally {
            Throwable cleanupFailure = null;
            try {
                if (scenario != null) scenario.close();
                NativeRuntime.close();
                restorePreferences(preferences, originalPreferences);
                if (temporary != null && temporary.exists() && !temporary.delete()) throw new IllegalStateException("临时集合清理失败");
                if (damaged != null && damaged.exists() && !damaged.delete()) throw new IllegalStateException("损坏夹具清理失败");
                if (rootExisted) {
                    byte[] current = rootConfig.exists() ? boundedRead(rootConfig, 4L << 20) : new byte[0];
                    if (!java.util.Arrays.equals(originalRoot, current)) restoreFile(rootConfig, originalRoot);
                } else if (rootConfig.exists() && !rootConfig.delete()) {
                    throw new IllegalStateException("测试创建的默认集合清理失败");
                }
                report.put("restored_private_config_and_preferences", true);
            } catch (Throwable failure) {
                cleanupFailure = failure;
                report.put("restored_private_config_and_preferences", false).put("cleanup_failure_type", failure.getClass().getName());
                report.put("status", "failed");
            }
            writeReport();
            writeVerification();
            if (primary == null && cleanupFailure != null) throw new AssertionError("测试环境未完整恢复", cleanupFailure);
        }
    }

    private void verifyHttpGet() throws Exception {
        stage = "HTTP GET 可见执行";
        JSONObject before = metrics(NETWORK_METRICS);
        openRequest("HTTP GET"); clickLabel("执行"); waitCompleted();
        reveal("HTTP 响应", false); reveal("AOT HTTP 验收成功", false);
        requirePattern("\\\"operation\\\"\\s*:\\s*\\\"GET\\\"", UI_TIMEOUT_MS);
        requirePattern("状态 200", UI_TIMEOUT_MS);
        JSONObject after = metrics(NETWORK_METRICS);
        equalCounters(before, after, "HTTP GET 不能记为写入", "http_posts", "modbus_writes");
        passed("http_get", "普通请求表单→执行→HTTP 200 与夹具正文", before, after);
        capture("HTTP_GET_真实响应"); returnToList();
    }

    private void verifyHttpPostCancelAndConfirm() throws Exception {
        stage = "HTTP POST 取消确认";
        JSONObject before = metrics(NETWORK_METRICS);
        openRequest("HTTP POST"); clickLabel("执行");
        requireText("确认执行写操作", UI_TIMEOUT_MS);
        reveal("127.0.0.1:48416/status", false); reveal("aot-local-verify", false);
        capture("HTTP_POST_写入确认"); clickLabel("取消");
        waitAbsent("确认执行写操作", UI_TIMEOUT_MS);
        long deadline = SystemClock.elapsedRealtime() + 1200;
        while (SystemClock.elapsedRealtime() < deadline) {
            equalCounters(before, metrics(NETWORK_METRICS), "取消不得发送 HTTP POST", "http_posts");
            SystemClock.sleep(120);
        }
        report.put("http_post_cancelled_without_write", true);
        stage = "HTTP POST 明确确认";
        clickLabel("执行"); requireText("确认执行写操作", UI_TIMEOUT_MS); clickLabel("确认执行");
        waitCompleted(); reveal("AOT HTTP 验收成功", false);
        requirePattern("\\\"operation\\\"\\s*:\\s*\\\"POST\\\"", UI_TIMEOUT_MS);
        JSONObject after = metrics(NETWORK_METRICS);
        assertEquals("确认后只能发送一次 HTTP POST", before.getLong("http_posts") + 1, after.getLong("http_posts"));
        report.put("http_post_confirmed_once", true);
        passed("http_post", "可见取消无写入；再次执行并确认恰好一次；显示 POST 成功正文", before, after);
        capture("HTTP_POST_确认后结果"); returnToList();
    }

    private void verifyMqtt() throws Exception {
        stage = "MQTT 明确读取一条消息";
        JSONObject before = metrics(NETWORK_METRICS);
        openRequest("MQTT"); clickLabel("执行"); waitCompleted();
        reveal("MQTT 消息", false); reveal("/sensors//temp/", false);
        requirePattern("\\\"value\\\"\\s*:\\s*23", UI_TIMEOUT_MS); requireText("°C", UI_TIMEOUT_MS);
        JSONObject after = metrics(NETWORK_METRICS);
        assertTrue("MQTT 夹具必须观察到真实数据包", after.getLong("mqtt_packets_received") > before.getLong("mqtt_packets_received"));
        equalCounters(before, after, "MQTT 读取不能触发其他协议写入", "http_posts", "modbus_writes");
        passed("mqtt", "执行 read-one→精确主题 /sensors//temp/→value 23 与单位 °C", before, after);
        capture("MQTT_保留消息"); returnToList();
    }

    private void verifyKafka() throws Exception {
        stage = "Kafka 明确消费一条记录";
        JSONObject before = metrics(KAFKA_METRICS);
        openRequest("Kafka"); clickLabel("执行"); waitCompleted();
        reveal("Kafka 记录", false); reveal("mobile-records", false); reveal("fixture-first", false);
        JSONObject after = metrics(KAFKA_METRICS);
        assertTrue("Kafka 夹具必须观察到真实请求", after.getLong("total_requests") > before.getLong("total_requests"));
        equalCounters(before, after, "消费不能生产或删除数据", "produce_requests", "delete_topic_requests");
        passed("kafka", "执行 earliest/limit1 消费→mobile-records→fixture-first", before, after);
        capture("Kafka_真实记录"); returnToList();
    }

    private void verifyModbus() throws Exception {
        stage = "Modbus 明确读取 holding 0..1";
        JSONObject before = metrics(NETWORK_METRICS);
        openRequest("Modbus"); clickLabel("执行"); waitCompleted();
        reveal("寄存器", false);
        requirePattern("\\\"u16\\\"\\s*:\\s*7(?:[,\\s}])", UI_TIMEOUT_MS);
        requirePattern("\\\"u16\\\"\\s*:\\s*42(?:[,\\s}])", UI_TIMEOUT_MS);
        JSONObject after = metrics(NETWORK_METRICS);
        assertEquals("有限 Modbus 请求只能读取一次", before.getLong("modbus_reads") + 1, after.getLong("modbus_reads"));
        equalCounters(before, after, "Modbus 读取不能写入设备", "modbus_writes", "http_posts");
        passed("modbus", "执行 unit1 holding[0..1]→可见原始字 7 / 42→读取计数 +1", before, after);
        capture("Modbus_真实寄存器"); returnToList();
    }

    private void verifyOpc() throws Exception {
        stage = "OPC UA 明确浏览测试对象";
        JSONObject before = metrics(OPC_METRICS);
        openRequest("OPC UA"); clickLabel("执行"); waitCompleted(); reveal("Temperature", false);
        JSONObject after = metrics(OPC_METRICS);
        assertTrue("OPC 夹具必须观察到真实浏览", after.getLong("browses") > before.getLong("browses"));
        equalCounters(before, after, "浏览不能执行写入或方法", "writes", "calls");
        passed("opcua", "执行 browse ns=1;i=85→Temperature→浏览计数增长", before, after);
        capture("OPC_UA_真实浏览");
    }

    private JSONObject collection() throws Exception {
        JSONArray requests = new JSONArray();
        requests.put(request("HTTP GET", "http", "GET", "http://127.0.0.1:48416/status", new JSONObject()));
        requests.put(request("HTTP POST", "http", "POST", "http://127.0.0.1:48416/status", new JSONObject().put("json", new JSONObject().put("operation", "aot-local-verify"))));
        requests.put(request("MQTT", "mqtt", "read-one", "mqtt://127.0.0.1:48414", new JSONObject().put("topic", "/sensors//temp/").put("limit", 1).put("qos", 0).put("client_id", "aot-" + suffix)));
        requests.put(request("Kafka", "kafka", "consume", "127.0.0.1:48412", new JSONObject().put("topic", "mobile-records").put("partition", 0).put("offset", "earliest").put("limit", 1)));
        requests.put(request("Modbus", "modbus", "read-holding", "tcp://127.0.0.1:48415", new JSONObject().put("unit", 1).put("address", 0).put("count", 2).put("samples", 1).put("word_order", "ABCD")));
        requests.put(request("OPC UA", "opcua", "browse", "opc.tcp://127.0.0.1:48410", new JSONObject().put("node_id", "ns=1;i=85").put("security_policy", "None").put("security_mode", "None").put("allow_insecure", true).put("max_references", 100)));
        return new JSONObject().put("version", 1).put("profiles", new JSONObject().put("local", new JSONObject())).put("requests", requests);
    }

    private JSONObject request(String label, String protocol, String action, String endpoint, JSONObject params) throws Exception {
        return new JSONObject().put("id", "aot-" + protocol + "-" + label.replace(' ', '-').toLowerCase() + "-" + suffix)
            .put("name", name(label)).put("protocol", protocol).put("action", action).put("endpoint", endpoint).put("timeout", "15s").put("params", params);
    }
    private String name(String label) { return "AOT " + label + " " + suffix; }

    private void openRequest(String label) throws Exception {
        requireText("请求工作台", UI_TIMEOUT_MS);
        reveal(name(label), true);
        requireText("表单", UI_TIMEOUT_MS);
        requireText("执行", UI_TIMEOUT_MS);
    }
    private void returnToList() throws Exception {
        UiObject2 back = exact("返回请求列表");
        if (back != null) clickObject(back); else device.pressBack();
        requireText("请求工作台", UI_TIMEOUT_MS);
    }
    private void waitCompleted() throws Exception {
        long end = SystemClock.elapsedRealtime() + UI_TIMEOUT_MS;
        while (SystemClock.elapsedRealtime() < end) {
            if (contains("执行失败") != null) throw new AssertionError("Flutter 明确显示协议执行失败：" + stage);
            if (contains("已完成") != null) return;
            SystemClock.sleep(100);
        }
        throw new AssertionError("等待 Flutter 完成状态超时：" + stage);
    }
    private UiObject2 exact(String label) {
        UiObject2 object = device.findObject(By.text(label));
        if (object == null) object = device.findObject(By.desc(label));
        if (object == null) {
            Pattern lines = Pattern.compile("(?s)(?:^|.*\\n)" + Pattern.quote(label) + "(?:\\n.*|$)");
            object = device.findObject(By.desc(lines));
        }
        return object;
    }
    private UiObject2 contains(String text) {
        UiObject2 object = device.findObject(By.textContains(text));
        return object == null ? device.findObject(By.descContains(text)) : object;
    }
    private UiObject2 pattern(String regex) {
        Pattern match = Pattern.compile("(?s).*" + regex + ".*");
        UiObject2 object = device.findObject(By.text(match));
        return object == null ? device.findObject(By.desc(match)) : object;
    }
    private UiObject2 requireText(String text, long timeout) {
        long end = SystemClock.elapsedRealtime() + timeout;
        do {
            UiObject2 object = contains(text);
            if (object != null) return object;
            SystemClock.sleep(100);
        } while (SystemClock.elapsedRealtime() < end);
        throw new AssertionError("可访问界面未出现预期文本：" + text);
    }
    private void requirePattern(String regex, long timeout) {
        long end = SystemClock.elapsedRealtime() + timeout;
        do {
            if (pattern(regex) != null) return;
            if (scroll(Direction.DOWN)) SystemClock.sleep(120); else SystemClock.sleep(100);
        } while (SystemClock.elapsedRealtime() < end);
        throw new AssertionError("可访问协议结果未包含预期字段：" + stage);
    }
    private void waitAbsent(String label, long timeout) {
        long end = SystemClock.elapsedRealtime() + timeout;
        while (SystemClock.elapsedRealtime() < end) {
            if (exact(label) == null) return;
            SystemClock.sleep(100);
        }
        throw new AssertionError("对话框未关闭：" + label);
    }
    private void clickLabel(String label) {
        long end = SystemClock.elapsedRealtime() + UI_TIMEOUT_MS;
        do {
            UiObject2 object = exact(label);
            if (object != null && object.isEnabled()) { clickObject(object); return; }
            SystemClock.sleep(100);
        } while (SystemClock.elapsedRealtime() < end);
        throw new AssertionError("找不到可点击的明确操作：" + label);
    }
    private void clickObject(UiObject2 object) {
        // 只使用可访问控件及其可点击父节点；没有坐标推测，也不重试不确定的点击。
        UiObject2 target = object;
        for (int depth = 0; depth < 6 && target != null && !target.isClickable(); depth++) target = target.getParent();
        if (target == null || !target.isClickable()) throw new AssertionError("语义节点没有可点击操作");
        target.click();
        SystemClock.sleep(200);
    }
    private void reveal(String text, boolean click) {
        UiObject2 object = contains(text);
        if (object == null) {
            for (int i = 0; i < 8 && scroll(Direction.UP); i++) SystemClock.sleep(100);
            for (int i = 0; i < 18; i++) {
                object = contains(text);
                if (object != null) break;
                if (!scroll(Direction.DOWN)) break;
                SystemClock.sleep(120);
            }
        }
        if (object == null) object = requireText(text, UI_TIMEOUT_MS);
        if (click) clickObject(object);
    }
    private boolean scroll(Direction direction) {
        List<UiObject2> scrolls = new ArrayList<>(device.findObjects(By.scrollable(true)));
        scrolls.sort(Comparator.comparingInt((UiObject2 o) -> o.getVisibleBounds().height()).reversed());
        for (UiObject2 object : scrolls) {
            if (!PACKAGE.equals(object.getApplicationPackage()) || object.getVisibleBounds().isEmpty()) continue;
            if (object.scroll(direction, 0.75f)) return true;
        }
        return false;
    }

    private JSONObject metrics(String endpoint) throws Exception {
        assertTrue("只允许读取已知回环夹具计数", endpoint.equals(NETWORK_METRICS) || endpoint.equals(OPC_METRICS) || endpoint.equals(KAFKA_METRICS));
        URI uri = URI.create(endpoint);
        assertEquals("127.0.0.1", uri.getHost());
        assertEquals("/metrics", uri.getPath());
        try (Socket socket = new Socket()) {
            socket.connect(new InetSocketAddress(InetAddress.getByAddress(new byte[]{127,0,0,1}), uri.getPort()), 3000);
            socket.setSoTimeout(3000);
            socket.getOutputStream().write(("GET /metrics HTTP/1.0\r\nHost: 127.0.0.1:" + uri.getPort() + "\r\nConnection: close\r\n\r\n").getBytes(StandardCharsets.US_ASCII));
            socket.getOutputStream().flush();
            String response = new String(read(socket.getInputStream(), 64L << 10), StandardCharsets.UTF_8);
            assertTrue("计数夹具必须直接返回成功", response.startsWith("HTTP/1.0 200 ") || response.startsWith("HTTP/1.1 200 "));
            int body = response.indexOf("\r\n\r\n"); assertTrue("计数响应缺少头部边界", body > 0);
            assertFalse("HTTP/1.0夹具不应使用分块编码", response.substring(0, body).toLowerCase(java.util.Locale.ROOT).contains("transfer-encoding"));
            return new JSONObject(response.substring(body + 4));
        }
    }
    private static void equalCounters(JSONObject before, JSONObject after, String message, String... keys) throws Exception {
        for (String key : keys) assertEquals(message + " / " + key, before.getLong(key), after.getLong(key));
    }
    private void passed(String protocol, String path, JSONObject before, JSONObject after) throws Exception {
        protocols.put(protocol, new JSONObject().put("status", "passed").put("visible_path", path).put("counter_before", before).put("counter_after", after));
        writeReport();
    }
    private void capture(String label) throws Exception {
        String prefix = String.format(java.util.Locale.ROOT, "%02d-%s-%s", ++artifactIndex, suffix, label);
        assertTrue("实际设备截图保存失败", device.takeScreenshot(new File(evidence, prefix + ".png")));
        device.dumpWindowHierarchy(new File(evidence, prefix + ".xml"));
        JSONArray artifacts = report.optJSONArray("artifacts"); if (artifacts == null) { artifacts = new JSONArray(); report.put("artifacts", artifacts); }
        artifacts.put(new JSONObject().put("stage", stage).put("screenshot", prefix + ".png").put("window_xml", prefix + ".xml"));
        writeReport();
    }
    private void writeReport() throws Exception {
        if (evidence == null) return;
        try (FileOutputStream output = new FileOutputStream(new File(evidence, "report.json"))) {
            output.write(report.toString(2).getBytes(StandardCharsets.UTF_8)); output.getFD().sync();
        }
    }
    private void writeVerification() throws Exception {
        JSONObject verified = new JSONObject();
        verified.put("schema", 1).put("status", report.optString("status", "failed"));
        verified.put("test_run_id", suffix);
        verified.put("apk_sha256", report.opt("apk_sha256"));
        verified.put("build_sha", report.opt("build_sha"));
        verified.put("go_version", report.opt("go_version"));
        verified.put("debuggable", report.has("debuggable") ? report.get("debuggable") : JSONObject.NULL);
        verified.put("abi", Build.SUPPORTED_ABIS.length == 0 ? "unknown" : Build.SUPPORTED_ABIS[0]);
        verified.put("supported_abis", new JSONArray(Build.SUPPORTED_ABIS));
        verified.put("sdk", Build.VERSION.SDK_INT).put("device", report.opt("device"));
        verified.put("activity", MainActivity.class.getName()).put("target_package", PACKAGE);
        verified.put("normal_entry", report.opt("normal_entry"));
        verified.put("http_post_cancelled_without_write", report.optBoolean("http_post_cancelled_without_write", false));
        verified.put("http_post_confirmed_once", report.optBoolean("http_post_confirmed_once", false));
        verified.put("startup_recovery_cancel_preserved_original", report.optBoolean("startup_recovery_cancel_preserved_original", false));
        verified.put("startup_recovery_opened_valid_collection", report.optBoolean("startup_recovery_opened_valid_collection", false));
        verified.put("startup_recovery_damaged_file_unchanged", report.optBoolean("startup_recovery_damaged_file_unchanged", false));
        JSONObject passed = new JSONObject();
        passed.put("http", pathPassed("http_get") && pathPassed("http_post"));
        for (String protocol : new String[]{"mqtt", "kafka", "modbus", "opcua"}) passed.put(protocol, pathPassed(protocol));
        verified.put("protocols", passed);
        verified.put("restored_private_config_and_preferences", report.optBoolean("restored_private_config_and_preferences", false));
        verified.put("details", "report.json").put("artifacts", report.opt("artifacts"));
        try (FileOutputStream output = new FileOutputStream(new File(evidence, "aot-verification.json"))) {
            output.write(verified.toString(2).getBytes(StandardCharsets.UTF_8)); output.getFD().sync();
        }
    }
    private boolean pathPassed(String protocol) {
        JSONObject item = protocols.optJSONObject(protocol);
        return item != null && "passed".equals(item.optString("status"));
    }
    private static JSONObject nativeData(String raw) throws Exception {
        JSONObject envelope = new JSONObject(raw);
        assertTrue("本机构建元数据读取失败", envelope.optBoolean("ok"));
        return envelope.getJSONObject("data");
    }
    private static String sha256(File file) throws Exception {
        MessageDigest digest = MessageDigest.getInstance("SHA-256"); byte[] buffer = new byte[65536];
        try (InputStream input = new FileInputStream(file)) { for (int n; (n = input.read(buffer)) != -1;) digest.update(buffer, 0, n); }
        StringBuilder result = new StringBuilder(); for (byte value : digest.digest()) result.append(String.format(java.util.Locale.ROOT, "%02x", value & 255));
        return result.toString();
    }
    private static byte[] boundedRead(File file, long limit) throws Exception { try (InputStream input = new FileInputStream(file)) { return read(input, limit); } }
    private static byte[] read(InputStream input, long limit) throws Exception {
        ByteArrayOutputStream output = new ByteArrayOutputStream(); byte[] buffer = new byte[8192]; long count = 0;
        for (int n; (n = input.read(buffer)) != -1;) { count += n; if (count > limit) throw new IllegalStateException("有界文件或夹具响应超过上限"); output.write(buffer, 0, n); }
        return output.toByteArray();
    }
    private static void restoreFile(File target, byte[] bytes) throws Exception {
        File stage = File.createTempFile("aot-restore-", ".tmp", target.getParentFile());
        try {
            try (FileOutputStream output = new FileOutputStream(stage)) { output.write(bytes); output.getFD().sync(); }
            Files.move(stage.toPath(), target.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
        } finally { if (stage.exists() && !stage.delete()) throw new IllegalStateException("恢复临时文件清理失败"); }
    }
    @SuppressWarnings("unchecked")
    private static void restorePreferences(SharedPreferences preferences, Map<String, ?> original) {
        SharedPreferences.Editor edit = preferences.edit().clear();
        for (Map.Entry<String, ?> entry : original.entrySet()) {
            Object value = entry.getValue(); String key = entry.getKey();
            if (value instanceof String) edit.putString(key, (String) value);
            else if (value instanceof Boolean) edit.putBoolean(key, (Boolean) value);
            else if (value instanceof Integer) edit.putInt(key, (Integer) value);
            else if (value instanceof Long) edit.putLong(key, (Long) value);
            else if (value instanceof Float) edit.putFloat(key, (Float) value);
            else if (value instanceof Set) edit.putStringSet(key, new HashSet<>((Set<String>) value));
            else throw new IllegalStateException("无法恢复未知类型的测试前设置");
        }
        assertTrue("原设置恢复失败", edit.commit());
    }
}
