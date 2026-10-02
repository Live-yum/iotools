import 'runtime.dart';
import 'package:http/browser_client.dart';
import 'platform/gateway.dart';
import 'platform/gateway_files_web.dart';

AppRuntime createRuntime() {
  if (!isLoopbackOrigin(Uri.base)) {
    return const AppRuntime(
      engine: UnavailableGatewayEngine(),
      platform: UnavailableGatewayPlatform(),
    );
  }
  final transport = GatewayTransport(
    origin: Uri.base,
    client: BrowserClient()..withCredentials = true,
  );
  return AppRuntime(
    engine: GatewayEngine(transport),
    platform: GatewayPlatform(transport, BrowserGatewayFiles(transport)),
  );
}
