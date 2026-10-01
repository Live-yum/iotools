import 'package:flutter/foundation.dart';

/// Adapter boundary: commands return the Go envelope's unwrapped data or throw.
abstract class ModbusHost implements Listenable {
  Map<String, dynamic> get request;
  Map<String, dynamic> get state;
  List<Map<String, dynamic>> get events;
  bool get readOnly;
  String get resultRunId;
  Map<String, dynamic>? get resultRequest;
  Map<String, dynamic>? get originalResultRequest;
  Stream<Map<String, dynamic>> get eventStream;
  Future<dynamic> command(Map<String, dynamic> command);
  void prepare(Map<String, dynamic> request);
  Future<void> saveRequest(Map<String, dynamic> request);
  Future<void> reviewAndRun(Map<String, dynamic> request);
  Future<void> exportText(String name, String text);
  void started(Map<String, dynamic> response);
  Future<void> collectionSaved(Map<String, dynamic> state);

  /// Immutable original submitted request, never the currently edited draft.
  Map<String, dynamic>? originalRequest(String runId) =>
      runId == resultRunId ? originalResultRequest : null;
}
