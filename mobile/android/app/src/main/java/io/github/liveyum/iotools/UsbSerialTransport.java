package io.github.liveyum.iotools;

import android.app.PendingIntent;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.hardware.usb.UsbDevice;
import android.hardware.usb.UsbDeviceConnection;
import android.hardware.usb.UsbManager;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;

import com.hoho.android.usbserial.driver.UsbSerialDriver;
import com.hoho.android.usbserial.driver.UsbSerialPort;
import com.hoho.android.usbserial.driver.UsbSerialProber;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.IOException;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Comparator;
import java.util.List;
import java.util.Locale;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.locks.ReentrantLock;

/**
 * Android USB-host transport for a single explicitly selected RTU serial port.
 * Enumeration only reads Android device descriptors. Permission, opening and
 * sending remain separate user actions; nothing auto-opens or auto-retries.
 * Native engine callbacks run off the UI thread. Physical adapter behavior must
 * still be verified on hardware, including automatic RS-485 direction control.
 */
public final class UsbSerialTransport {
    public interface Listener { void onStatus(String message); }
    private static final Object STATE = new Object();
    private static final ReentrantLock IO = new ReentrantLock();
    private static final int MAX_FRAME = 256;
    private static final int MAX_TIMEOUT_MS = 5000;
    private static final String PERMISSION_ACTION = "io.github.liveyum.iotools.USB_PERMISSION." + UUID.randomUUID();
    private static Context context;
    private static UsbManager manager;
    private static Listener listener;
    private static Session current;
    private static long generation;
    private static boolean paused = true;
    private static String status = "USB 串口未打开；真实硬件尚未验证";

    private UsbSerialTransport() {}

    public static void initialize(Context source, Listener nextListener) {
        synchronized (STATE) {
            listener = nextListener;
            if (context != null) return;
            context = source.getApplicationContext();
            manager = (UsbManager) context.getSystemService(Context.USB_SERVICE);
            IntentFilter filter = new IntentFilter(PERMISSION_ACTION);
            filter.addAction(UsbManager.ACTION_USB_DEVICE_DETACHED);
            if (Build.VERSION.SDK_INT >= 33) context.registerReceiver(RECEIVER, filter, Context.RECEIVER_NOT_EXPORTED);
            else context.registerReceiver(RECEIVER, filter);
            paused = false;
        }
    }

    private static final BroadcastReceiver RECEIVER = new BroadcastReceiver() {
        @Override public void onReceive(Context ignored, Intent intent) {
            UsbDevice device = intent.getParcelableExtra(UsbManager.EXTRA_DEVICE);
            if (device == null) return;
            if (PERMISSION_ACTION.equals(intent.getAction())) {
                UsbManager usb;
                synchronized (STATE) { usb = manager; }
                // Recheck the OS permission; never trust a broadcast alone.
                if (usb != null && usb.hasPermission(device)) notifyStatus("USB 权限已允许，请显式打开所选端口");
                else notifyStatus("USB 权限未允许，未打开端口");
            } else if (UsbManager.ACTION_USB_DEVICE_DETACHED.equals(intent.getAction())) {
                Session selected;
                synchronized (STATE) { selected = current; }
                if (selected != null && selected.deviceId == device.getDeviceId()) {
                    closeSelected(selected, "USB 设备已断开；请重新选择并打开");
                }
            }
        }
    };

    /** Lists known serial ports only; does not open a device or request permission. */
    public static JSONArray listPorts() throws IOException {
        UsbManager usb = usbManager();
        List<UsbSerialDriver> drivers = new ArrayList<>(UsbSerialProber.getDefaultProber().findAllDrivers(usb));
        drivers.sort(Comparator.comparingInt(d -> d.getDevice().getDeviceId()));
        JSONArray result = new JSONArray();
        try {
            for (UsbSerialDriver driver : drivers) {
                UsbDevice device = driver.getDevice();
                for (int port = 0; port < driver.getPorts().size(); port++) {
                    if (result.length() >= 256) return result;
                    String endpoint = endpoint(device.getDeviceId(), port);
                    String label = String.format(Locale.ROOT, "%s · %04X:%04X · 端口 %d", driver.getClass().getSimpleName(), device.getVendorId(), device.getProductId(), port);
                    result.put(new JSONObject().put("endpoint", endpoint).put("label", label)
                            .put("permission", usb.hasPermission(device)).put("device_id", device.getDeviceId())
                            .put("port", port).put("vid", device.getVendorId()).put("pid", device.getProductId()));
                }
            }
        } catch (JSONException e) { throw new IOException("USB 设备描述失败", e); }
        return result;
    }

    /** Returns true when already permitted; false when an OS permission dialog was requested. */
    public static boolean requestPermission(String endpoint) throws IOException {
        UsbManager usb = usbManager();
        UsbSerialPort port = findPort(usb, endpoint);
        synchronized (STATE) {
            if (paused || context == null) throw new IOException("应用在后台，不能申请 USB 权限");
            if (usb.hasPermission(port.getDevice())) return true;
            Intent intent = new Intent(PERMISSION_ACTION).setPackage(context.getPackageName());
            PendingIntent permission = PendingIntent.getBroadcast(context, port.getDevice().getDeviceId(), intent, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
            usb.requestPermission(port.getDevice(), permission);
        }
        notifyStatus("等待 Android USB 设备权限");
        return false;
    }

    /** Explicitly opens the selected, already-permitted port. Call off the UI thread. */
    public static void open(String endpoint, int baud, int dataBits, int stopBits, String parity) throws IOException {
        requireWorker();
        int parityValue = validateSettings(baud, dataBits, stopBits, parity);
        lockIO(MAX_TIMEOUT_MS);
        UsbDeviceConnection connection = null;
        UsbSerialPort port = null;
        try {
            UsbManager usb = usbManager();
            long expectedGeneration;
            synchronized (STATE) {
                if (paused) throw new IOException("应用在后台，USB 端口未打开");
                if (current != null) throw new IOException("请先关闭当前 USB 端口，再打开新端口");
                expectedGeneration = generation;
            }
            port = findPort(usb, endpoint);
            if (!usb.hasPermission(port.getDevice())) throw new IOException("请先允许所选 USB 设备的 Android 权限");
            connection = usb.openDevice(port.getDevice());
            if (connection == null) throw new IOException("无法打开 USB 设备，请检查权限和连接");
            port.open(connection);
            port.setParameters(baud, dataBits, stopBits, parityValue);
            synchronized (STATE) {
                if (paused || generation != expectedGeneration || current != null) throw new IOException("USB 打开已取消");
                current = new Session(endpoint, port, connection, baud, dataBits, stopBits, parityValue);
            }
            port = null;
            connection = null;
            notifyStatus("USB 已打开 " + endpoint + "；尚未发送 Modbus 请求");
        } catch (SecurityException | IllegalArgumentException | UnsupportedOperationException e) {
            throw new IOException("USB 打开失败：" + e.getMessage(), e);
        } finally {
            if (port != null) try { port.close(); } catch (Exception ignored) {}
            if (connection != null) connection.close();
            IO.unlock();
        }
    }

    /** JNI entry: one bounded RTU transaction, never an automatic retry. */
    public static byte[] exchange(String endpoint, byte[] request, int baud, int dataBits, int stopBits, String parity, int timeoutMs) throws IOException {
        requireWorker();
        validateExchange(request, timeoutMs);
        int parityValue = validateSettings(baud, dataBits, stopBits, parity);
        long deadline = SystemClock.elapsedRealtime() + timeoutMs;
        lockIO(timeoutMs);
        Session selected = null;
        try {
            synchronized (STATE) {
                selected = current;
                if (paused || selected == null) throw new IOException("请先显式打开 USB 端口");
                if (!selected.endpoint.equals(endpoint)) throw new IOException("请求的 USB 端口与已打开端口不同");
                if (selected.baud != baud || selected.dataBits != dataBits || selected.stopBits != stopBits || selected.parity != parityValue)
                    throw new IOException("串口参数与已打开端口不同，请关闭后按新参数重新打开");
            }
            Session serial = selected;
            ensureOpen(serial);
            // A quiet interval prevents stale frames from being mistaken for this reply.
            // Any buffered data is reported, never silently consumed as a response.
            byte[] buffer = new byte[MAX_FRAME + 1];
            int silentMs = Math.max(2, (int) Math.ceil(3500.0 * (1 + dataBits + stopBits + (parityValue == 0 ? 0 : 1)) / baud));
            if (remaining(deadline) < silentMs) throw new IOException("USB 超时不足以等待 RTU 帧间隔");
            if (serial.port.read(buffer, silentMs) != 0) throw new IOException("USB 收到残留数据，请关闭并检查线路后重开");
            ensureOpen(serial);
            serial.port.write(request, remaining(deadline));
            return readFrame(serial.port, deadline, silentMs, () -> ensureOpen(serial));
        } catch (IOException | RuntimeException e) {
            if (selected != null) closeSelected(selected, "USB 事务已停止；请检查线路并重新打开端口");
            if (e instanceof IOException) throw (IOException) e;
            throw new IOException("USB 通信失败：" + e.getMessage(), e);
        } finally { IO.unlock(); }
    }

    interface OpenCheck { void check() throws IOException; }

    // Package-visible for hardware-free framing/cancellation instrumentation tests.
    static byte[] readFrame(UsbSerialPort port, long deadline, int silentMs, OpenCheck openCheck) throws IOException {
        byte[] buffer = new byte[MAX_FRAME + 1];
        byte[] frame = new byte[MAX_FRAME];
        int count = 0;
        while (true) {
            openCheck.check();
            boolean raw = responseLength(frame, count) == -1;
            int left = remaining(deadline);
            int readTimeout = Math.min(raw ? silentMs : 50, left);
            int read = port.read(buffer, readTimeout);
            openCheck.check();
            if (read == 0) {
                // A full RTU silent interval, not the overall deadline, ends a raw frame.
                if (raw && count >= 4 && left >= silentMs) return Arrays.copyOf(frame, count);
                continue;
            }
            if (read < 0 || count + read > MAX_FRAME) throw new IOException("USB RTU 响应超过 256 字节");
            System.arraycopy(buffer, 0, frame, count, read);
            count += read;
            int expected = responseLength(frame, count);
            if (expected > MAX_FRAME || (expected > 0 && count > expected)) throw new IOException("USB RTU 响应长度无效");
            if (expected > 0 && count == expected) return Arrays.copyOf(frame, count);
        }
    }

    static void validateExchange(byte[] request, int timeoutMs) throws IOException {
        if (request == null || request.length < 4 || request.length > MAX_FRAME) throw new IOException("USB RTU 请求必须为 4..256 字节");
        if (timeoutMs < 1 || timeoutMs > MAX_TIMEOUT_MS) throw new IOException("USB 超时必须为 1..5000 毫秒");
        int unit = request[0] & 255;
        if (unit < 1 || unit > 247) throw new IOException("USB RTU 禁用广播，单元号必须为 1..247");
    }

    /** Returns 0 for an incomplete known header, -1 for raw-function framing. */
    static int responseLength(byte[] frame, int count) throws IOException {
        if (count < 2) return 0;
        int function = frame[1] & 255;
        if ((function & 128) != 0) return 5;
        switch (function) {
            case 1: case 2: case 3: case 4: case 23:
                return count < 3 ? 0 : 5 + (frame[2] & 255);
            case 5: case 6: case 15: case 16: return 8;
            case 43:
                if (count < 8) return 0;
                if ((frame[2] & 255) != 14) throw new IOException("不支持的 RTU MEI 响应");
                int offset = 8;
                int objects = frame[7] & 255;
                for (int i = 0; i < objects; i++) {
                    if (offset + 2 > MAX_FRAME - 2) throw new IOException("RTU 设备标识响应过长");
                    if (count < offset + 2) return 0;
                    offset += 2 + (frame[offset + 1] & 255);
                    if (offset > MAX_FRAME - 2) throw new IOException("RTU 设备标识响应过长");
                    if (count < offset) return 0;
                }
                return offset + 2;
            default: return -1;
        }
    }

    public static String status() { synchronized (STATE) { return status; } }
    public static void close() {
        Session selected;
        synchronized (STATE) { generation++; selected = current; current = null; }
        if (selected != null) selected.close();
        notifyStatus("USB 端口已关闭；重新运行需显式打开");
    }
    public static void pause() { synchronized (STATE) { paused = true; } close(); }
    public static void resume() { synchronized (STATE) { if (context != null) paused = false; } }
    public static void shutdown() {
        pause();
        synchronized (STATE) {
            if (context != null) { try { context.unregisterReceiver(RECEIVER); } catch (IllegalArgumentException ignored) {} }
            context = null; manager = null; listener = null;
        }
    }

    private static void closeSelected(Session selected, String message) {
        synchronized (STATE) {
            if (current != selected) return;
            current = null; generation++;
        }
        selected.close();
        notifyStatus(message);
    }
    private static void notifyStatus(String message) {
        final Listener callback;
        synchronized (STATE) { status = message; callback = listener; }
        if (callback != null) new Handler(Looper.getMainLooper()).post(() -> {
            synchronized (STATE) { if (listener != callback) return; }
            callback.onStatus(message);
        });
    }
    private static UsbManager usbManager() throws IOException {
        synchronized (STATE) {
            if (manager == null) throw new IOException("此设备不支持 Android USB host 或尚未初始化");
            return manager;
        }
    }
    private static UsbSerialPort findPort(UsbManager usb, String requested) throws IOException {
        if (requested == null || !requested.matches("usb://(?:0|[1-9][0-9]{0,9})/(?:0|[1-9][0-9]{0,2})")) throw new IOException("请从已连接 USB 设备中选择端口");
        for (UsbSerialDriver driver : UsbSerialProber.getDefaultProber().findAllDrivers(usb))
            for (int i = 0; i < driver.getPorts().size(); i++)
                if (requested.equals(endpoint(driver.getDevice().getDeviceId(), i))) return driver.getPorts().get(i);
        throw new IOException("所选 USB 串口已断开或没有受支持的驱动");
    }
    private static String endpoint(int device, int port) { return "usb://" + device + "/" + port; }
    private static int validateSettings(int baud, int dataBits, int stopBits, String parity) throws IOException {
        if (baud < 300 || baud > 4000000 || dataBits != 8 || (stopBits != 1 && stopBits != 2)) throw new IOException("RTU 参数：波特率 300..4000000，8 数据位，1 或 2 停止位");
        if ("N".equalsIgnoreCase(parity)) return UsbSerialPort.PARITY_NONE;
        if ("E".equalsIgnoreCase(parity)) return UsbSerialPort.PARITY_EVEN;
        if ("O".equalsIgnoreCase(parity)) return UsbSerialPort.PARITY_ODD;
        throw new IOException("RTU 校验位必须为 N、E 或 O");
    }
    private static void requireWorker() throws IOException {
        if (Looper.myLooper() == Looper.getMainLooper()) throw new IOException("USB 操作必须在后台工作线程执行");
    }
    private static void lockIO(int timeoutMs) throws IOException {
        try { if (!IO.tryLock(timeoutMs, TimeUnit.MILLISECONDS)) throw new IOException("USB 端口忙，等待超时"); }
        catch (InterruptedException e) { Thread.currentThread().interrupt(); throw new IOException("USB 操作已取消", e); }
    }
    private static int remaining(long deadline) throws IOException {
        long value = deadline - SystemClock.elapsedRealtime();
        if (value <= 0) throw new IOException("USB RTU 事务超时；不自动重试");
        return (int) Math.min(MAX_TIMEOUT_MS, value);
    }
    private static void ensureOpen(Session selected) throws IOException {
        synchronized (STATE) { if (paused || current != selected || selected.closed.get()) throw new IOException("USB 事务已取消或设备已关闭"); }
    }
    private static final class Session {
        final String endpoint;
        final UsbSerialPort port;
        final UsbDeviceConnection connection;
        final int deviceId, baud, dataBits, stopBits, parity;
        final AtomicBoolean closed = new AtomicBoolean();
        Session(String endpoint, UsbSerialPort port, UsbDeviceConnection connection, int baud, int dataBits, int stopBits, int parity) {
            this.endpoint = endpoint; this.port = port; this.connection = connection;
            this.deviceId = port.getDevice().getDeviceId(); this.baud = baud; this.dataBits = dataBits; this.stopBits = stopBits; this.parity = parity;
        }
        void close() {
            if (!closed.compareAndSet(false, true)) return;
            // Closing the Android connection first interrupts any concurrent read/write.
            connection.close();
            try { port.close(); } catch (Exception ignored) {}
        }
    }
}
