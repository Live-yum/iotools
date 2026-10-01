package io.github.liveyum.iotools;
import java.nio.charset.StandardCharsets;
/** Structured UTF-8 bridge to the shared protocol engine. */
public final class NativeRuntime {
 static {System.loadLibrary("iotools");}
 private NativeRuntime(){}
 private static native byte[] openBytes(byte[] path,int flags);
 private static native byte[] commandBytes(byte[] command);
 private static native void lifecycle(int action);
 private static String decode(byte[] bytes){return bytes==null||bytes.length==0?"{\"ok\":false,\"error\":\"本地引擎未返回结果\"}":new String(bytes,StandardCharsets.UTF_8);}
 public static String open(String path,String version,boolean readOnly,boolean history){return decode(openBytes(path.getBytes(StandardCharsets.UTF_8),(readOnly?1:0)|(history?2:0)));}
 public static String command(String json){return decode(commandBytes(json.getBytes(StandardCharsets.UTF_8)));}
 public static void pause(){lifecycle(0);}
 public static void resume(){lifecycle(1);}
 public static void close(){lifecycle(2);}
}
