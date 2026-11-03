#include <jni.h>
#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>

typedef int (*bridge_version_fn)(char *, int);
typedef int (*bridge_start_fn)(char *, char *, int);
typedef int (*bridge_status_fn)(char *, int);

static void *go_handle = NULL;
static bridge_version_fn go_version = NULL;
static bridge_start_fn go_start = NULL;
static bridge_status_fn go_status = NULL;
static bridge_status_fn go_stop = NULL;

static jstring error_json(JNIEnv *env, const char *code) {
    char buf[160];
    snprintf(buf, sizeof(buf), "{\"state\":\"failed\",\"errorCode\":\"%s\"}", code);
    return (*env)->NewStringUTF(env, buf);
}

// 只按 basename dlopen：Go 产物没有 SONAME，且 Android 会按路径重复映射，
// 双载会初始化第二个 Go runtime 和它自己的 bridge 全局。
static int ensure_loaded(void) {
    if (go_handle != NULL) {
        return 1;
    }
    void *h = dlopen("libaether_mobilebridge.so", RTLD_NOW | RTLD_LOCAL);
    if (h == NULL) {
        return 0;
    }
    bridge_version_fn v = (bridge_version_fn)dlsym(h, "nativeBridgeVersion");
    bridge_start_fn s = (bridge_start_fn)dlsym(h, "nativeStart");
    bridge_status_fn g = (bridge_status_fn)dlsym(h, "nativeGetStatus");
    bridge_status_fn t = (bridge_status_fn)dlsym(h, "nativeStop");
    if (v == NULL || s == NULL || g == NULL || t == NULL) {
        dlclose(h);
        return 0;
    }
    go_handle = h;
    go_version = v;
    go_start = s;
    go_status = g;
    go_stop = t;
    return 1;
}

static jstring one_buffer_call(JNIEnv *env, bridge_status_fn call) {
    int need = call(NULL, 0);
    int size = need < 0 ? -need : 0;
    if (size <= 0) {
        return error_json(env, "bridge_buffer_size");
    }
    char *buf = (char *)malloc((size_t)size);
    if (buf == NULL) {
        return error_json(env, "bridge_oom");
    }
    int written = call(buf, size);
    jstring out = NULL;
    if (written > 0 && written <= size) {
        out = (*env)->NewStringUTF(env, buf);
    }
    free(buf);
    if (out == NULL) {
        return error_json(env, "bridge_call_failed");
    }
    return out;
}

static jstring start_call(JNIEnv *env, const char *config) {
    int need = go_start((char *)config, NULL, 0);
    int size = need < 0 ? -need : 0;
    if (size <= 0) {
        return error_json(env, "bridge_buffer_size");
    }
    char *buf = (char *)malloc((size_t)size);
    if (buf == NULL) {
        return error_json(env, "bridge_oom");
    }
    int written = go_start((char *)config, buf, size);
    jstring out = NULL;
    if (written > 0 && written <= size) {
        out = (*env)->NewStringUTF(env, buf);
    }
    free(buf);
    if (out == NULL) {
        return error_json(env, "bridge_call_failed");
    }
    return out;
}

JNIEXPORT jint JNICALL JNI_OnLoad(JavaVM *vm, void *reserved) {
    (void)vm;
    (void)reserved;
    if (!ensure_loaded()) {
        return JNI_ERR;
    }
    return JNI_VERSION_1_6;
}

JNIEXPORT void JNICALL JNI_OnUnload(JavaVM *vm, void *reserved) {
    (void)vm;
    (void)reserved;
    if (go_handle != NULL) {
        dlclose(go_handle);
        go_handle = NULL;
    }
}

JNIEXPORT jstring JNICALL
Java_com_muyuchat_aether_runtime_NativeBridge_nativeBridgeVersion(JNIEnv *env, jclass cls) {
    (void)cls;
    if (!ensure_loaded()) {
        return error_json(env, "bridge_unavailable");
    }
    return one_buffer_call(env, go_version);
}

JNIEXPORT jstring JNICALL
Java_com_muyuchat_aether_runtime_NativeBridge_nativeStart(JNIEnv *env, jclass cls, jstring config) {
    (void)cls;
    if (config == NULL) {
        return error_json(env, "invalid_config");
    }
    if (!ensure_loaded()) {
        return error_json(env, "bridge_unavailable");
    }
    const char *utf = (*env)->GetStringUTFChars(env, config, NULL);
    if (utf == NULL) {
        return error_json(env, "invalid_config");
    }
    jstring out = start_call(env, utf);
    (*env)->ReleaseStringUTFChars(env, config, utf);
    return out;
}

JNIEXPORT jstring JNICALL
Java_com_muyuchat_aether_runtime_NativeBridge_nativeGetStatus(JNIEnv *env, jclass cls) {
    (void)cls;
    if (!ensure_loaded()) {
        return error_json(env, "bridge_unavailable");
    }
    return one_buffer_call(env, go_status);
}

JNIEXPORT jstring JNICALL
Java_com_muyuchat_aether_runtime_NativeBridge_nativeStop(JNIEnv *env, jclass cls) {
    (void)cls;
    if (!ensure_loaded()) {
        return error_json(env, "bridge_unavailable");
    }
    return one_buffer_call(env, go_stop);
}
