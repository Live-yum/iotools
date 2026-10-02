import Darwin
import Flutter
import UIKit
import XCTest
@testable import Runner

final class RunnerTests: XCTestCase {
  private func temporaryRoot() throws -> URL {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("iotools-ios-test-" + UUID().uuidString)
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: false)
    addTeardownBlock { try? FileManager.default.removeItem(at: root) }
    return root
  }

  func testExportLimitsAndUTF8() throws {
    let invalidLimits: [Any] = [0, -1, 1.5, Double.infinity, Double.nan, NSNumber(value: true), "1", 8_589_934_593]
    for limit in invalidLimits {
      XCTAssertThrowsError(try ExportRequest(["text": "x", "limit": limit]))
    }
    XCTAssertThrowsError(try ExportRequest(["path": "a", "text": "x"]))
    XCTAssertThrowsError(try ExportRequest(["text": "x", "name": ".."]))
    let root = try temporaryRoot()
    let valid = try ExportRequest(["text": "真实😀", "name": "a/b\nc.txt", "limit": 10])
    let staged = try ExportStaging.prepare(valid, root: root, check: {})
    defer { try? FileManager.default.removeItem(at: staged.directory) }
    XCTAssertEqual(staged.file.lastPathComponent, "a_b_c.txt")
    XCTAssertEqual(try String(contentsOf: staged.file, encoding: .utf8), "真实😀")
    let tooSmall = try ExportRequest(["text": "真实😀", "limit": 9])
    XCTAssertThrowsError(try ExportStaging.prepare(tooSmall, root: root, check: {}))
  }

  func testExportPrivateBoundaryAndSize() throws {
    let root = try temporaryRoot()
    let folder = root.appendingPathComponent("nested")
    try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: false)
    let source = folder.appendingPathComponent("a.txt")
    try Data("private".utf8).write(to: source)
    let outside = try temporaryRoot()
    try Data("outside".utf8).write(to: outside.appendingPathComponent("b.txt"))
    try FileManager.default.createSymbolicLink(at: root.appendingPathComponent("link"), withDestinationURL: outside)
    try FileManager.default.createSymbolicLink(at: root.appendingPathComponent("file-link"), withDestinationURL: source)
    for path in ["", "/tmp/a", "../a", "nested/../a", "nested//a", "nested/./a", "a\\b", "a:b", "a\0b", "link/b.txt", "file-link", "nested"] {
      XCTAssertThrowsError(try ExportStaging.openPrivateFile(path, root: root), path)
    }
    let rootLink = outside.appendingPathComponent("root-link")
    try FileManager.default.createSymbolicLink(at: rootLink, withDestinationURL: root)
    XCTAssertThrowsError(try ExportStaging.openPrivateFile("nested/a.txt", root: rootLink))
    let fifo = root.appendingPathComponent("pipe")
    XCTAssertEqual(mkfifo(fifo.path, mode_t(0o600)), 0)
    XCTAssertThrowsError(try ExportStaging.openPrivateFile("pipe", root: root))
    let valid = try ExportRequest(["path": "nested/a.txt", "limit": 7])
    let staged = try ExportStaging.prepare(valid, root: root, check: {})
    defer { try? FileManager.default.removeItem(at: staged.directory) }
    XCTAssertEqual(try Data(contentsOf: staged.file), Data("private".utf8))
    XCTAssertThrowsError(try ExportStaging.prepare(try ExportRequest(["path": "nested/a.txt", "limit": 6]), root: root, check: {}))
  }

  func testCancelledExportCleansStaging() throws {
    let root = try temporaryRoot()
    let before = Set(try FileManager.default.contentsOfDirectory(atPath: FileManager.default.temporaryDirectory.path).filter { $0.hasPrefix("iotools-export-") })
    let request = try ExportRequest(["text": "test"])
    var checks = 0
    XCTAssertThrowsError(try ExportStaging.prepare(request, root: root) {
      checks += 1
      if checks > 1 { throw ExportFailure.cancelled }
    })
    let after = Set(try FileManager.default.contentsOfDirectory(atPath: FileManager.default.temporaryDirectory.path).filter { $0.hasPrefix("iotools-export-") })
    XCTAssertEqual(before, after)
  }

  @MainActor
  func testRealExportPickerCancelBusyAndStaleDelegate() throws {
    let host = AppDelegate()
    let window = UIWindow(frame: UIScreen.main.bounds)
    let presenter = UIViewController()
    window.rootViewController = presenter
    host.window = window
    window.makeKeyAndVisible()
    defer { host.cancelExport(); window.isHidden = true }
    var completions = 0
    var finalValue: Any?
    host.beginExport(["text": "iOS picker acceptance", "name": "iotools-test.txt"]) {
      completions += 1; finalValue = $0
    }
    var busy: FlutterError?
    host.beginExport(["text": "must not overlap"]) { busy = $0 as? FlutterError }
    XCTAssertEqual(busy?.code, "busy")
    let ready = expectation(description: "Real UIDocumentPicker is presented")
    func poll(_ remaining: Int) {
      if presenter.presentedViewController is UIDocumentPickerViewController { ready.fulfill(); return }
      guard remaining > 0 else { return }
      DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) { poll(remaining - 1) }
    }
    poll(100)
    wait(for: [ready], timeout: 12)
    let picker = try XCTUnwrap(presenter.presentedViewController as? UIDocumentPickerViewController)
    XCTAssertTrue(picker.allowsMultipleSelection == false)
    let screenshot = UIGraphicsImageRenderer(bounds: window.bounds).image { _ in
      window.drawHierarchy(in: window.bounds, afterScreenUpdates: true)
    }
    let attachment = XCTAttachment(image: screenshot)
    attachment.name = "actual-system-export-picker"
    attachment.lifetime = .keepAlways
    add(attachment)
    host.documentPickerWasCancelled(picker)
    XCTAssertEqual(completions, 1)
    XCTAssertNil(finalValue)
    picker.dismiss(animated: false)

    // A late callback from this picker must not complete the next generation.
    host.beginExport(["text": String(repeating: "x", count: 1024)]) { _ in completions += 1 }
    host.documentPicker(picker, didPickDocumentsAt: [URL(fileURLWithPath: "/obsolete")])
    XCTAssertEqual(completions, 1)
    host.cancelExport()
    XCTAssertEqual(completions, 2)
    host.documentPickerWasCancelled(picker)
    XCTAssertEqual(completions, 2)
  }

  func testActualProcessGoABIAndLifecycle() throws {
    typealias Version = @convention(c) () -> UInt32
    typealias Open = @convention(c) (UnsafePointer<UInt8>?, Int, UnsafePointer<UInt8>?, Int, UInt32, UnsafeMutablePointer<UnsafeMutablePointer<UInt8>?>?, UnsafeMutablePointer<Int>?) -> UInt64
    typealias Command = @convention(c) (UInt64, UnsafePointer<UInt8>?, Int, UnsafeMutablePointer<UnsafeMutablePointer<UInt8>?>?, UnsafeMutablePointer<Int>?) -> Int32
    typealias Lifecycle = @convention(c) (UInt64, Int32) -> Int32
    typealias Free = @convention(c) (UnsafeMutablePointer<UInt8>?) -> Void
    let process = try XCTUnwrap(dlopen(nil, RTLD_NOW))
    defer { dlclose(process) }
    func symbol<T>(_ name: String, _ type: T.Type) throws -> T {
      let address = try XCTUnwrap(dlsym(process, name), "Missing process FFI export: \(name)")
      return unsafeBitCast(address, to: type)
    }
    let version = try symbol("IotoolsNativeABIVersion", Version.self)
    let open = try symbol("IotoolsNativeOpen", Open.self)
    let command = try symbol("IotoolsNativeCommand", Command.self)
    let lifecycle = try symbol("IotoolsNativeLifecycle", Lifecycle.self)
    let free = try symbol("IotoolsNativeFree", Free.self)
    XCTAssertEqual(version(), 1)
    XCTAssertNil(dlsym(process, "IotoolsUSBExchange"))
    var output: UnsafeMutablePointer<UInt8>?
    var size = 0
    func decode() throws -> [String: Any] {
      let bytes = try XCTUnwrap(output)
      defer { free(bytes); output = nil; size = 0 }
      guard size > 0 && size <= 16 * 1024 * 1024 else { throw NSError(domain: "InvalidNativeReplySize", code: size) }
      return try XCTUnwrap(JSONSerialization.jsonObject(with: Data(bytes: bytes, count: size)) as? [String: Any])
    }
    let root = try temporaryRoot().resolvingSymlinksInPath()
    let path = Array(root.appendingPathComponent("真实-😀.yaml").path.utf8)
    let directory = Array(root.path.utf8)
    let handle = path.withUnsafeBufferPointer { pathBytes in
      directory.withUnsafeBufferPointer { rootBytes in
        open(pathBytes.baseAddress, path.count, rootBytes.baseAddress, directory.count, 0, &output, &size)
      }
    }
    XCTAssertNotEqual(handle, 0)
    defer { _ = lifecycle(handle, 2) }
    XCTAssertEqual(try decode()["ok"] as? Bool, true)
    func state() throws -> [String: Any] {
      let json = Array("{\"op\":\"state\"}".utf8)
      let status = json.withUnsafeBufferPointer { command(handle, $0.baseAddress, json.count, &output, &size) }
      XCTAssertEqual(status, 0)
      let reply = try decode()
      XCTAssertEqual(reply["ok"] as? Bool, true)
      return try XCTUnwrap(reply["data"] as? [String: Any])
    }
    XCTAssertEqual(try state()["closed"] as? Bool, false)
    XCTAssertEqual(lifecycle(handle, 0), 0)
    XCTAssertEqual(try state()["paused"] as? Bool, true)
    XCTAssertEqual(lifecycle(handle, 1), 0)
    XCTAssertEqual(try state()["paused"] as? Bool, false)
    XCTAssertEqual(lifecycle(handle, 2), 0)
    XCTAssertEqual(lifecycle(handle, 2), 2)
  }
}
