package io.github.liveyum.iotools;
public final class NativeRuntime {
 static {System.loadLibrary("iotools");}
 private NativeRuntime(){}
 public static native int start(String path,int cols,int rows,int flags);
 public static native byte[] read();
 public static native byte[] frame();
 public static native int paste(byte[] data);
 public static native int key(String name,int rune,int modifiers);
 public static native int mouse(int x,int y,boolean down);
 public static native int input(byte[] data);
 public static native int resize(int cols,int rows);
 public static native void stop();
 public static native void pause();
 public static native void resume();
 public static native void options(int flags,String path);
 public static native int validate(String path);
 public static native int state();
 public static native String error();
 public static native String clipboard();
 public static native int importConfig(String staged,String target);
}
