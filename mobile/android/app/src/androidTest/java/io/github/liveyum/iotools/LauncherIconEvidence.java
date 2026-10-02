package io.github.liveyum.iotools;

import static org.junit.Assert.*;
import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Path;
import android.graphics.RectF;
import android.graphics.drawable.AdaptiveIconDrawable;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.LayerDrawable;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.security.MessageDigest;
import java.util.Locale;
import org.json.JSONArray;
import org.json.JSONObject;

/** Actual Android resource rendering; only included in the instrumentation APK. */
final class LauncherIconEvidence {
    private static final String ORIGINAL_SHA = "3527d1e406ee0f5f0d572c6f77fcf592d3cf34378bfa9dc5ec61c8fb2e87380b";
    private static final int SIZE = 216;
    static JSONObject verify(Context context, File directory) throws Exception {
        assertEquals("Launcher must use the requested icon resource", R.mipmap.ic_launcher, context.getApplicationInfo().icon);
        ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        try (InputStream source = context.getResources().openRawResource(R.drawable.iotools_avatar)) {
            byte[] buffer = new byte[8192]; int count;
            while ((count = source.read(buffer)) != -1) bytes.write(buffer, 0, count);
        }
        StringBuilder digest = new StringBuilder();
        for (byte value : MessageDigest.getInstance("SHA-256").digest(bytes.toByteArray())) digest.append(String.format(Locale.ROOT, "%02x", value & 255));
        assertEquals("User artwork bytes must be preserved", ORIGINAL_SHA, digest.toString());
        Drawable installed = context.getPackageManager().getApplicationIcon(context.getPackageName());
        assertTrue("API26+ launcher must expose an adaptive icon", installed instanceof AdaptiveIconDrawable);
        AdaptiveIconDrawable adaptive = (AdaptiveIconDrawable) installed;
        adaptive.setBounds(0, 0, SIZE, SIZE);
        assertInsideCircle(adaptive.getForeground(), SIZE * 33f / 72f);
        LayerDrawable legacy = (LayerDrawable) context.getDrawable(R.drawable.ic_launcher_legacy);
        assertNotNull(legacy); legacy.setBounds(0, 0, SIZE, SIZE);
        assertInsideCircle(legacy.getDrawable(1), SIZE / 2f);
        Drawable round = context.getDrawable(R.mipmap.ic_launcher_round);
        assertTrue("Round launcher icon must preserve adaptive resources", round instanceof AdaptiveIconDrawable);
        JSONArray previews = new JSONArray();
        for (boolean circle : new boolean[]{true, false}) {
            String shape = circle ? "circle" : "rounded-square";
            String name = "launcher-adaptive-" + shape + ".png";
            render(adaptive.getBackground(), adaptive.getForeground(), circle, new File(directory, name)); previews.put(name);
            name = "launcher-legacy-" + shape + ".png";
            render(legacy.getDrawable(0), legacy.getDrawable(1), circle, new File(directory, name)); previews.put(name);
        }
        return new JSONObject().put("status", "passed").put("original_sha256", ORIGINAL_SHA)
            .put("foreground_inside_safe_circle", true).put("previews", previews);
    }
    private static void assertInsideCircle(Drawable foreground, float radius) {
        Bitmap pixels = Bitmap.createBitmap(SIZE, SIZE, Bitmap.Config.ARGB_8888);
        foreground.draw(new Canvas(pixels)); int visible = 0;
        for (int y = 0; y < SIZE; y++) for (int x = 0; x < SIZE; x++) {
            if (Color.alpha(pixels.getPixel(x, y)) == 0) continue;
            visible++;
            double dx = x + .5 - SIZE / 2.0, dy = y + .5 - SIZE / 2.0;
            assertTrue("Hat/body pixel outside launcher safe circle", dx * dx + dy * dy <= radius * radius);
        }
        assertTrue("Requested artwork must be visible", visible > SIZE * SIZE / 10);
        pixels.recycle();
    }
    private static void render(Drawable background, Drawable foreground, boolean circle, File file) throws Exception {
        Bitmap bitmap = Bitmap.createBitmap(SIZE, SIZE, Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(bitmap); Path mask = new Path();
        if (circle) mask.addCircle(SIZE / 2f, SIZE / 2f, SIZE / 2f, Path.Direction.CW);
        else mask.addRoundRect(new RectF(0, 0, SIZE, SIZE), SIZE * .22f, SIZE * .22f, Path.Direction.CW);
        canvas.clipPath(mask); background.draw(canvas); foreground.draw(canvas);
        try (FileOutputStream output = new FileOutputStream(file)) { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)); }
        bitmap.recycle();
    }
}
