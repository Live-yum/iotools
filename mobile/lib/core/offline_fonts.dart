export 'offline_fonts_stub.dart'
    if (dart.library.io) 'offline_fonts_native.dart'
    if (dart.library.js_interop) 'offline_fonts_web.dart';
