//go:build android

#include <jni.h>
#include <stdlib.h>
#include "_cgo_export.h"

JNIEXPORT jint JNICALL Java_io_github_liveyum_iotools_NativeRuntime_start(JNIEnv *env,jclass cls,jstring path,jint cols,jint rows,jint flags){
 const char *p=(*env)->GetStringUTFChars(env,path,0);if(!p)return -1;
 int result=IotoolsStart((char*)p,cols,rows,flags);(*env)->ReleaseStringUTFChars(env,path,p);return result;
}
JNIEXPORT jbyteArray JNICALL Java_io_github_liveyum_iotools_NativeRuntime_read(JNIEnv *env,jclass cls){
 char data[65536];int n=IotoolsRead(data,sizeof(data));if(n<0||n>65536)n=0;
 jbyteArray out=(*env)->NewByteArray(env,n);if(out&&n)(*env)->SetByteArrayRegion(env,out,0,n,(jbyte*)data);return out;
}
JNIEXPORT jint JNICALL Java_io_github_liveyum_iotools_NativeRuntime_input(JNIEnv *env,jclass cls,jbyteArray data){
 jsize n=(*env)->GetArrayLength(env,data);if(n>65536)return -1;
 jbyte *p=(*env)->GetByteArrayElements(env,data,0);if(!p)return -1;
 int result=IotoolsInput((char*)p,n);(*env)->ReleaseByteArrayElements(env,data,p,JNI_ABORT);return result;
}
JNIEXPORT jint JNICALL Java_io_github_liveyum_iotools_NativeRuntime_resize(JNIEnv *env,jclass cls,jint cols,jint rows){return IotoolsResize(cols,rows);}
JNIEXPORT void JNICALL Java_io_github_liveyum_iotools_NativeRuntime_stop(JNIEnv *env,jclass cls){IotoolsStop();}
JNIEXPORT jint JNICALL Java_io_github_liveyum_iotools_NativeRuntime_state(JNIEnv *env,jclass cls){return IotoolsState();}
JNIEXPORT jstring JNICALL Java_io_github_liveyum_iotools_NativeRuntime_error(JNIEnv *env,jclass cls){char *p=IotoolsError();jstring s=(*env)->NewStringUTF(env,p);free(p);return s;}
JNIEXPORT jint JNICALL Java_io_github_liveyum_iotools_NativeRuntime_importConfig(JNIEnv *env,jclass cls,jstring stage,jstring target){
 const char *a=(*env)->GetStringUTFChars(env,stage,0);const char *b=(*env)->GetStringUTFChars(env,target,0);
 int r=-1;if(a&&b)r=IotoolsImport((char*)a,(char*)b);
 if(a)(*env)->ReleaseStringUTFChars(env,stage,a);if(b)(*env)->ReleaseStringUTFChars(env,target,b);return r;
}

JNIEXPORT jstring JNICALL Java_io_github_liveyum_iotools_NativeRuntime_clipboard(JNIEnv *env,jclass cls){char *p=IotoolsClipboard();jstring s=(*env)->NewStringUTF(env,p);free(p);return s;}

JNIEXPORT void JNICALL Java_io_github_liveyum_iotools_NativeRuntime_pause(JNIEnv *env,jclass cls){IotoolsPause();}
JNIEXPORT void JNICALL Java_io_github_liveyum_iotools_NativeRuntime_resume(JNIEnv *env,jclass cls){IotoolsResume();}
JNIEXPORT void JNICALL Java_io_github_liveyum_iotools_NativeRuntime_options(JNIEnv *env,jclass cls,jint flags,jstring path){const char*p=(*env)->GetStringUTFChars(env,path,0);if(p){IotoolsOptions(flags,(char*)p);(*env)->ReleaseStringUTFChars(env,path,p);}}
JNIEXPORT jint JNICALL Java_io_github_liveyum_iotools_NativeRuntime_validate(JNIEnv *env,jclass cls,jstring path){const char*p=(*env)->GetStringUTFChars(env,path,0);if(!p)return -1;int r=IotoolsValidate((char*)p);(*env)->ReleaseStringUTFChars(env,path,p);return r;}

JNIEXPORT jbyteArray JNICALL Java_io_github_liveyum_iotools_NativeRuntime_frame(JNIEnv *env,jclass cls){int n=0;void*p=IotoolsFrame(&n);if(n<0||n>4*1024*1024){free(p);return (*env)->NewByteArray(env,0);}jbyteArray out=(*env)->NewByteArray(env,n);if(out&&n&&p)(*env)->SetByteArrayRegion(env,out,0,n,(jbyte*)p);free(p);return out;}
JNIEXPORT jint JNICALL Java_io_github_liveyum_iotools_NativeRuntime_paste(JNIEnv *env,jclass cls,jbyteArray data){jsize n=(*env)->GetArrayLength(env,data);if(n>65536)return -1;jbyte*p=(*env)->GetByteArrayElements(env,data,0);if(!p)return -1;int r=IotoolsPaste((char*)p,n);(*env)->ReleaseByteArrayElements(env,data,p,JNI_ABORT);return r;}
JNIEXPORT jint JNICALL Java_io_github_liveyum_iotools_NativeRuntime_key(JNIEnv *env,jclass cls,jstring name,jint rune,jint mods){const char*p=(*env)->GetStringUTFChars(env,name,0);if(!p)return -1;int r=IotoolsKey((char*)p,rune,mods);(*env)->ReleaseStringUTFChars(env,name,p);return r;}
JNIEXPORT jint JNICALL Java_io_github_liveyum_iotools_NativeRuntime_mouse(JNIEnv *env,jclass cls,jint x,jint y,jboolean down){return IotoolsMouse(x,y,down?1:0);}
