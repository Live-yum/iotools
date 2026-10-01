package io.github.liveyum.iotools;

import static org.junit.Assert.*;

import android.os.SystemClock;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import com.hoho.android.usbserial.driver.UsbSerialPort;
import java.io.IOException;
import java.lang.reflect.Proxy;
import java.util.ArrayDeque;
import java.util.Arrays;
import java.util.Queue;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import org.junit.runner.RunWith;

/** These tests exercise bounded parsing and fail-closed behavior, not USB hardware. */
@RunWith(AndroidJUnit4.class)
public final class UsbSerialTransportTest {
    private static UsbSerialPort port(byte[]... reads) {
        Queue<byte[]> queue = new ArrayDeque<>(Arrays.asList(reads));
        return (UsbSerialPort) Proxy.newProxyInstance(UsbSerialPort.class.getClassLoader(), new Class<?>[]{UsbSerialPort.class}, (proxy, method, args) -> {
            if (!method.getName().equals("read")) throw new AssertionError("Unexpected USB action: " + method.getName());
            int timeout = (Integer) args[args.length - 1];
            assertTrue("read timeout must be bounded", timeout > 0 && timeout <= 50);
            byte[] chunk = queue.poll();
            if (chunk == null) throw new IOException("scripted input exhausted");
            System.arraycopy(chunk, 0, (byte[]) args[0], 0, chunk.length);
            return chunk.length;
        });
    }

    @Test public void fragmentedStandardReplyUsesDeclaredByteCount() throws Exception {
        byte[] expected = {1, 3, 2, 0x12, 0x34, 0, 0};
        byte[] frame = UsbSerialTransport.readFrame(port(new byte[]{1}, new byte[]{3, 2}, new byte[]{0x12}, new byte[]{0x34, 0, 0}), SystemClock.elapsedRealtime() + 1000, 4, () -> {});
        assertArrayEquals(expected, frame);
    }

    @Test public void rawReplyWaitsForSilenceAfterAllFragments() throws Exception {
        byte[] expected = {1, 65, 4, 5, 6, 7};
        byte[] frame = UsbSerialTransport.readFrame(port(new byte[]{1, 65, 4}, new byte[]{5}, new byte[]{6, 7}, new byte[0]), SystemClock.elapsedRealtime() + 1000, 4, () -> {});
        assertArrayEquals(expected, frame);
    }

    @Test public void readRejectsOversizedAndConcatenatedFrames() throws Exception {
        for (byte[] data : new byte[][]{new byte[257], new byte[]{1, 3, 2, 1, 2, 0, 0, 99}}) {
            try { UsbSerialTransport.readFrame(port(data), SystemClock.elapsedRealtime() + 1000, 4, () -> {}); fail("oversized input accepted"); }
            catch (IOException expected) { assertTrue(expected.getMessage().contains("256") || expected.getMessage().contains("长度")); }
        }
    }

    @Test public void closeDuringReadPreventsResponseDelivery() throws Exception {
        AtomicInteger checks = new AtomicInteger();
        try {
            UsbSerialTransport.readFrame(port(new byte[]{1, (byte) 0x83, 2, 0, 0}), SystemClock.elapsedRealtime() + 1000, 4, () -> { if (checks.incrementAndGet() == 2) throw new IOException("cancelled"); });
            fail("delivered response after close");
        } catch (IOException expected) { assertEquals("cancelled", expected.getMessage()); }
    }

    @Test public void parsesDeviceIdentificationBoundaries() throws Exception {
        byte[] frame = {1, 43, 14, 1, 1, 0, 0, 1, 0, 3, 65, 66, 67, 0, 0};
        for (int count = 0; count < 13; count++) assertEquals(0, UsbSerialTransport.responseLength(frame, count));
        assertEquals(15, UsbSerialTransport.responseLength(frame, 13));
        assertEquals(5, UsbSerialTransport.responseLength(new byte[]{1, (byte) 0x83}, 2));
        assertEquals(8, UsbSerialTransport.responseLength(new byte[]{1, 16}, 2));
        frame[9] = (byte) 255;
        try { UsbSerialTransport.responseLength(frame, 10); fail("oversized object accepted"); }
        catch (IOException expected) { assertTrue(expected.getMessage().contains("过长")); }
    }

    @Test public void validationRejectsBroadcastAndUnboundedTimeoutWithoutIO() throws Exception {
        byte[] request = {1, 3, 0, 0, 0, 1, 0, 0};
        UsbSerialTransport.validateExchange(request, 5000);
        for (int timeout : new int[]{0, -1, 5001}) {
            try { UsbSerialTransport.validateExchange(request, timeout); fail("unbounded timeout accepted"); }
            catch (IOException expected) { assertTrue(expected.getMessage().contains("5000")); }
        }
        request[0] = 0;
        try { UsbSerialTransport.validateExchange(request, 1000); fail("broadcast accepted"); }
        catch (IOException expected) { assertTrue(expected.getMessage().contains("广播")); }
    }
}
