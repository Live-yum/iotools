import CoreFoundation
import Darwin
import Flutter
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate, UIDocumentPickerDelegate {
  private var exportChannel: FlutterMethodChannel?
  private var exportResult: FlutterResult?
  private var exportDirectory: URL?
  private weak var exportPicker: UIDocumentPickerViewController?
  private let exportLock = NSLock()
  private var exportGeneration: UInt64 = 0

  override func application(_ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
    GeneratedPluginRegistrant.register(with: self)
    if let controller = window?.rootViewController as? FlutterViewController {
      let channel = FlutterMethodChannel(name: "io.github.liveyum.iotools/platform", binaryMessenger: controller.binaryMessenger)
      exportChannel = channel
      channel.setMethodCallHandler { [weak self] call, result in
        guard let self = self else { result(FlutterError(code: "closed", message: "文件服务已关闭", details: nil)); return }
        switch call.method {
        case "files.export": self.beginExport(call.arguments, result: result)
        case "files.cancel": self.cancelExport(); result(nil)
        default: result(FlutterMethodNotImplemented)
        }
      }
    }
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  private func token() -> UInt64 { exportLock.lock(); defer { exportLock.unlock() }; return exportGeneration }
  private func invalidate() { exportLock.lock(); exportGeneration &+= 1; exportLock.unlock() }
  private func finishExport(_ value: Any?) {
    invalidate()
    let callback = exportResult; exportResult = nil
    exportPicker = nil
    if let directory = exportDirectory { try? FileManager.default.removeItem(at: directory) }
    exportDirectory = nil
    callback?(value)
  }
  func cancelExport() {
    let picker = exportPicker
    finishExport(nil)
    picker?.dismiss(animated: true)
  }
  func beginExport(_ arguments: Any?, result: @escaping FlutterResult) {
    guard exportResult == nil else { result(FlutterError(code: "busy", message: "已有文件导出正在进行", details: nil)); return }
    let request: ExportRequest
    do { request = try ExportRequest(arguments) }
    catch { result(FlutterError(code: "argument", message: (error as? ExportFailure)?.message, details: nil)); return }
    exportResult = result
    invalidate()
    let generation = token()
    DispatchQueue.global(qos: .userInitiated).async { [weak self] in
      guard let self = self else { return }
      do {
        // The root is chosen by the host, never supplied by a channel caller.
        let support = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
        let root = support.appendingPathComponent("iotools", isDirectory: true)
        let staged = try ExportStaging.prepare(request, root: root) {
          guard self.token() == generation else { throw ExportFailure.cancelled }
        }
        DispatchQueue.main.async {
          guard self.token() == generation else { try? FileManager.default.removeItem(at: staged.directory); return }
          guard let controller = self.window?.rootViewController, controller.presentedViewController == nil else {
            try? FileManager.default.removeItem(at: staged.directory)
            self.finishExport(FlutterError(code: "busy", message: "请先关闭当前系统弹窗再导出", details: nil)); return
          }
          self.exportDirectory = staged.directory
          let picker = UIDocumentPickerViewController(forExporting: [staged.file], asCopy: true)
          picker.delegate = self
          self.exportPicker = picker
          controller.present(picker, animated: true)
        }
      } catch {
        DispatchQueue.main.async {
          guard self.token() == generation else { return }
          self.finishExport(FlutterError(code: "export", message: (error as? ExportFailure)?.message ?? "文件导出失败，请检查文件和可用空间", details: nil))
        }
      }
    }
  }
  func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
    // A dismissed picker can deliver a late callback after another export begins.
    guard controller === exportPicker else { return }
    finishExport(nil)
  }
  func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
    guard controller === exportPicker else { return }
    finishExport(["exported": !urls.isEmpty])
  }
}

struct ExportRequest {
  let path: String?
  let text: String?
  let name: String
  let limit: Int64

  init(_ arguments: Any?) throws {
    guard let args = arguments as? [String: Any],
      let raw = (args["limit"] ?? NSNumber(value: 1024 * 1024 * 1024)) as? NSNumber,
      CFGetTypeID(raw) != CFBooleanGetTypeID(), raw.doubleValue.isFinite,
      raw.doubleValue.rounded(.towardZero) == raw.doubleValue,
      raw.doubleValue >= 1, raw.doubleValue <= Double(8 * 1024 * 1024 * 1024) else {
      throw ExportFailure.message("导出上限必须在 1 字节到 8 GiB 之间")
    }
    path = args["path"] as? String
    text = args["text"] as? String
    guard (path != nil) != (text != nil) else { throw ExportFailure.message("请选择文件或文本导出") }
    let proposed = (args["name"] as? String) ?? "iotools-export.txt"
    name = proposed.components(separatedBy: CharacterSet.controlCharacters.union(CharacterSet(charactersIn: "/\\:"))).joined(separator: "_")
    guard !name.isEmpty, name != ".", name != "..", name.utf8.count <= 240 else { throw ExportFailure.message("导出文件名无效") }
    limit = raw.int64Value
  }
}

enum ExportStaging {
  static func prepare(_ request: ExportRequest, root: URL, check: () throws -> Void) throws -> (directory: URL, file: URL) {
    try check()
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent("iotools-export-" + UUID().uuidString, isDirectory: true)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false)
    var complete = false
    defer { if !complete { try? FileManager.default.removeItem(at: directory) } }
    let destination = directory.appendingPathComponent(request.name, isDirectory: false)
    if let text = request.text {
      guard Int64(text.lengthOfBytes(using: .utf8)) <= request.limit else { throw ExportFailure.message("文本超过导出上限") }
      try check()
      try Data(text.utf8).write(to: destination, options: .atomic)
    } else if let path = request.path {
      let input = try openPrivateFile(path, root: root)
      defer { try? input.close() }
      var properties = stat()
      guard fstat(input.fileDescriptor, &properties) == 0,
        properties.st_mode & mode_t(S_IFMT) == mode_t(S_IFREG),
        properties.st_size >= 0, properties.st_size <= request.limit else { throw ExportFailure.message("文件不是普通文件或超过导出上限") }
      guard FileManager.default.createFile(atPath: destination.path, contents: nil) else { throw ExportFailure.message("无法创建导出暂存文件") }
      let output = try FileHandle(forWritingTo: destination)
      defer { try? output.close() }
      var total: Int64 = 0
      while true {
        try check()
        let data = try input.read(upToCount: 1024 * 1024) ?? Data()
        if data.isEmpty { break }
        total += Int64(data.count)
        guard total <= request.limit else { throw ExportFailure.message("文件在导出过程中超过上限") }
        try output.write(contentsOf: data)
      }
      guard total == properties.st_size else { throw ExportFailure.message("文件在导出过程中改变了大小，请重试") }
      try output.synchronize()
    }
    try check()
    complete = true
    return (directory, destination)
  }

  // Descriptor-relative traversal rejects links atomically, including a link
  // substituted after validation. Opening with O_NONBLOCK avoids a FIFO hang.
  static func openPrivateFile(_ relative: String, root: URL) throws -> FileHandle {
    let parts = relative.split(separator: "/", omittingEmptySubsequences: false)
    guard !relative.contains("\\"), !relative.contains("\0"), !relative.contains(":"),
      !parts.isEmpty, parts.allSatisfy({ !$0.isEmpty && $0 != "." && $0 != ".." }) else { throw ExportFailure.message("导出路径必须是应用内相对路径") }
    var parent = Darwin.open(root.path, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
    guard parent >= 0 else { throw ExportFailure.message("应用文件目录不可用或为符号链接") }
    defer { Darwin.close(parent) }
    for part in parts.dropLast() {
      let child = openat(parent, String(part), O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
      guard child >= 0 else { throw ExportFailure.message("导出路径不存在或包含符号链接") }
      Darwin.close(parent)
      parent = child
    }
    let descriptor = openat(parent, String(parts.last!), O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC)
    guard descriptor >= 0 else { throw ExportFailure.message("导出文件不存在或为符号链接") }
    var properties = stat()
    guard fstat(descriptor, &properties) == 0, properties.st_mode & mode_t(S_IFMT) == mode_t(S_IFREG) else {
      Darwin.close(descriptor)
      throw ExportFailure.message("只能导出普通文件")
    }
    return FileHandle(fileDescriptor: descriptor, closeOnDealloc: true)
  }
}

enum ExportFailure: Error {
  case message(String), cancelled
  var message: String { switch self { case .message(let value): return value; case .cancelled: return "导出已取消" } }
}
