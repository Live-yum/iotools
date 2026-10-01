//go:build android
#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include "_cgo_export.h"
static void init_usb(JNIEnv* env);
static jbyteArray response(JNIEnv* env,char* data){
 if(!data)return (*env)->NewByteArray(env,0);
 size_t n=strlen(data);if(n>16*1024*1024){free(data);return (*env)->NewByteArray(env,0);}
 jbyteArray result=(*env)->NewByteArray(env,(jsize)n);
 if(result&&n)(*env)->SetByteArrayRegion(env,result,0,(jsize)n,(jbyte*)data);
 free(data);return result;
}
JNIEXPORT jbyteArray JNICALL Java_io_github_liveyum_iotools_NativeRuntime_openBytes(JNIEnv* env,jclass cls,jbyteArray path,jint flags,jbyteArray privateRoot){
 init_usb(env);
 if(!path)return response(env,NULL);jsize n=(*env)->GetArrayLength(env,path);if(n<1||n>16384)return response(env,NULL);
 jbyte* data=(*env)->GetByteArrayElements(env,path,0);if(!data)return response(env,NULL);
 jsize root_n=privateRoot?(*env)->GetArrayLength(env,privateRoot):0;
 if(root_n<0||root_n>16384){(*env)->ReleaseByteArrayElements(env,path,data,JNI_ABORT);return response(env,NULL);}
 jbyte* root_data=root_n?(*env)->GetByteArrayElements(env,privateRoot,0):NULL;
 if(root_n&&!root_data){(*env)->ReleaseByteArrayElements(env,path,data,JNI_ABORT);return response(env,NULL);}
 char* result=IotoolsOpen((char*)data,n,flags,(char*)root_data,root_n);
 if(root_data)(*env)->ReleaseByteArrayElements(env,privateRoot,root_data,JNI_ABORT);
 (*env)->ReleaseByteArrayElements(env,path,data,JNI_ABORT);return response(env,result);
}
JNIEXPORT jbyteArray JNICALL Java_io_github_liveyum_iotools_NativeRuntime_commandBytes(JNIEnv* env,jclass cls,jbyteArray input){
 if(!input)return response(env,NULL);jsize n=(*env)->GetArrayLength(env,input);if(n<1||n>8*1024*1024)return response(env,NULL);
 jbyte* data=(*env)->GetByteArrayElements(env,input,0);if(!data)return response(env,NULL);
 char* result=IotoolsCommand((char*)data,n);(*env)->ReleaseByteArrayElements(env,input,data,JNI_ABORT);return response(env,result);
}
JNIEXPORT void JNICALL Java_io_github_liveyum_iotools_NativeRuntime_lifecycle(JNIEnv* env,jclass cls,jint action){IotoolsLifecycle(action);}

/* Retained application class reference avoids class-loader lookup on Go threads. */
static JavaVM* usb_vm;
static jclass usb_class;
static jmethodID usb_exchange;
static jmethodID usb_close;
static void init_usb(JNIEnv* env){
 if(usb_class)return;
 jclass local=(*env)->FindClass(env,"io/github/liveyum/iotools/UsbSerialTransport");
 if(!local){(*env)->ExceptionClear(env);return;}
 (*env)->GetJavaVM(env,&usb_vm);
 usb_class=(*env)->NewGlobalRef(env,local);(*env)->DeleteLocalRef(env,local);
 usb_exchange=(*env)->GetStaticMethodID(env,usb_class,"exchange","(Ljava/lang/String;[BIIILjava/lang/String;I)[B");
 usb_close=(*env)->GetStaticMethodID(env,usb_class,"close","()V");
 if((*env)->ExceptionCheck(env)){(*env)->ExceptionClear(env);usb_exchange=NULL;usb_close=NULL;}
}
static JNIEnv* usb_env(int* attached){
 *attached=0;if(!usb_vm)return NULL;JNIEnv* env=NULL;
 jint status=(*usb_vm)->GetEnv(usb_vm,(void**)&env,JNI_VERSION_1_6);
 if(status==JNI_EDETACHED){if((*usb_vm)->AttachCurrentThread(usb_vm,&env,NULL)!=JNI_OK)return NULL;*attached=1;}
 else if(status!=JNI_OK)return NULL;return env;
}
int IotoolsUSBExchange(char* endpoint,void* bytes,int n,int baud,int bits,int stops,char* parity,int timeout,void* output,int capacity){
 int attached=0;JNIEnv* env=usb_env(&attached);if(!env||!usb_exchange)return -1;
 if((*env)->PushLocalFrame(env,8)!=JNI_OK){if(attached)(*usb_vm)->DetachCurrentThread(usb_vm);return -2;}
 int result=-3;jstring name=(*env)->NewStringUTF(env,endpoint);jstring p=(*env)->NewStringUTF(env,parity);jbyteArray input=(*env)->NewByteArray(env,n);
 if(name&&p&&input){(*env)->SetByteArrayRegion(env,input,0,n,(jbyte*)bytes);
 jbyteArray data=(jbyteArray)(*env)->CallStaticObjectMethod(env,usb_class,usb_exchange,name,input,baud,bits,stops,p,timeout);
 if((*env)->ExceptionCheck(env)){(*env)->ExceptionClear(env);result=-4;}
 else if(data){jsize count=(*env)->GetArrayLength(env,data);if(count>=0&&count<=capacity){(*env)->GetByteArrayRegion(env,data,0,count,(jbyte*)output);result=count;}}
 }
 if((*env)->ExceptionCheck(env)){(*env)->ExceptionClear(env);result=-5;}
 (*env)->PopLocalFrame(env,NULL);if(attached)(*usb_vm)->DetachCurrentThread(usb_vm);return result;
}
void IotoolsUSBClose(void){
 int attached=0;JNIEnv* env=usb_env(&attached);if(!env||!usb_close)return;
 (*env)->CallStaticVoidMethod(env,usb_class,usb_close);if((*env)->ExceptionCheck(env))(*env)->ExceptionClear(env);
 if(attached)(*usb_vm)->DetachCurrentThread(usb_vm);
}
