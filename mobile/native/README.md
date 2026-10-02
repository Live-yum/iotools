# 原生协议内核构建输出

这里由 `scripts/flutter-platforms/build-native.sh` 生成平台库，不提交二进制。Flutter 桌面构建要求 `IOTOOLS_NATIVE_LIBRARY` 指向真实内核；没有内核会直接失败。Windows/Linux/macOS 使用随应用分发的共享库，iOS 静态链接同一个 C ABI。
