package io.github.liveyum.iotools;
public final class NativeRuntime {
 static {System.loadLibrary("iotools");}
 private NativeRuntime(){}
 public static native int start(String path,int cols,int rows);
 public static native byte[] read();
 public static native int input(byte[] data);
 public static native int resize(int cols,int rows);
 public static native void stop();
 public static native int state();
 public static native String error();
 public static native String clipboard();
 public static native int importConfig(String staged,String target);
}
