import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import '../../integration_test/modbus_interaction.dart';

void main() {
  testWidgets('reveal scrolls the visible list, ignoring a later offscreen list', (
    tester,
  ) async {
    WidgetController.hitTestWarningShouldBeFatal = true;
    addTearDown(() => WidgetController.hitTestWarningShouldBeFatal = false);
    tester.view.physicalSize = const Size(480, 752);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final visible = ScrollController(), offscreen = ScrollController();
    var taps = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Stack(
            children: [
              ListView.builder(
                controller: visible,
                itemExtent: 80,
                itemCount: 35,
                itemBuilder: (_, index) => index == 30
                    ? FilledButton(
                        onPressed: () => taps++,
                        child: const Text('读取设备标识'),
                      )
                    : ListTile(title: Text('可见页面 $index')),
              ),
              Positioned(
                left: 500,
                top: 0,
                bottom: 0,
                width: 480,
                child: ListView.builder(
                  controller: offscreen,
                  itemExtent: 80,
                  itemCount: 35,
                  itemBuilder: (_, index) => Text('相邻页面 $index'),
                ),
              ),
            ],
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    final all = find.byWidgetPredicate(
      (widget) =>
          widget is Scrollable && widget.axisDirection == AxisDirection.down,
    );
    expect(all, findsNWidgets(2));
    expect(tester.getCenter(all.last).dx, greaterThan(480));
    final action = find.text('读取设备标识');
    expect(action, findsNothing);
    await revealModbusFinder(tester, action);
    await tester.ensureVisible(action);
    await waitForModbusInteraction(tester, action);
    await tester.tap(action);
    await tester.pumpAndSettle();
    expect(taps, 1);
    expect(visible.offset, greaterThan(0));
    expect(offscreen.offset, 0);
    await tester.pumpWidget(const SizedBox());
    visible.dispose();
    offscreen.dispose();
  });
  for (final size in [const Size(480, 752), const Size(320, 640)]) {
    testWidgets('reveal respects nested clipping and fixed bar at $size', (
      tester,
    ) async {
      WidgetController.hitTestWarningShouldBeFatal = true;
      addTearDown(() => WidgetController.hitTestWarningShouldBeFatal = false);
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final outer = ScrollController(), inner = ScrollController();
      var taps = 0, bottomGestures = 0;
      await tester.pumpWidget(MaterialApp(home: Scaffold(
        extendBody: true,
        appBar: AppBar(title: const Text('嵌套页面')),
        bottomNavigationBar: Material(child: Listener(
          onPointerDown: (_) => bottomGestures++,
          child: const SizedBox(height: 128, child: Center(child: Text('固定底栏'))),
        )),
        body: SingleChildScrollView(
          controller: outer,
          child: Column(children: [
            const SizedBox(height: 300),
            SizedBox(height: 900, child: ListView.builder(
              controller: inner,
              itemExtent: 64,
              itemCount: 60,
              itemBuilder: (_, index) => index == 45
                  ? FilledButton(onPressed: () => taps++, child: const Text('嵌套目标'))
                  : Text('行 $index'),
            )),
          ]),
        ),
      )));
      await tester.pumpAndSettle();
      final scrollables = find.byWidgetPredicate((widget) =>
          widget is Scrollable && widget.axisDirection == AxisDirection.down);
      expect(tester.getCenter(scrollables.last).dy, greaterThan(size.height - 128));
      final target = find.text('嵌套目标');
      expect(target, findsNothing);
      await revealModbusFinder(tester, target);
      expect(inner.offset, greaterThan(0));
      expect(outer.offset, 0, reason: '滚动内层实际可见区域，不移动外层');
      expect(bottomGestures, 0, reason: '手势不能从固定底栏开始');
      await Scrollable.ensureVisible(tester.element(target), alignment: .5);
      await waitForModbusInteraction(tester, target);
      await tester.tap(target);
      await tester.pumpAndSettle();
      expect(taps, 1);
      await tester.pumpWidget(const SizedBox());
      outer.dispose(); inner.dispose();
    });

    testWidgets('reveal scrolls current dialog and leaves background fixed at $size', (
      tester,
    ) async {
      WidgetController.hitTestWarningShouldBeFatal = true;
      addTearDown(() => WidgetController.hitTestWarningShouldBeFatal = false);
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final background = ScrollController(), dialog = ScrollController();
      var taps = 0;
      await tester.pumpWidget(MaterialApp(home: Builder(builder: (context) => Scaffold(
        body: ListView(controller: background, children: [
          FilledButton(onPressed: () => showDialog<void>(context: context,
            builder: (context) => AlertDialog(
              title: const Text('当前对话框'),
              content: SizedBox(width: 400, height: 320, child: ListView.builder(
                controller: dialog,
                itemExtent: 64,
                itemCount: 30,
                itemBuilder: (_, index) => index == 25
                    ? FilledButton(onPressed: () => taps++, child: const Text('对话框目标'))
                    : Text('项目 $index'),
              )),
              actions: [TextButton(onPressed: () => Navigator.pop(context), child: const Text('关闭'))],
            )), child: const Text('打开对话框')),
          const SizedBox(height: 3000),
        ]),
      ))));
      await tester.tap(find.text('打开对话框'));
      await tester.pumpAndSettle();
      final target = find.text('对话框目标');
      expect(target, findsNothing);
      await revealModbusFinder(tester, target);
      await Scrollable.ensureVisible(tester.element(target), alignment: .5);
      await waitForModbusInteraction(tester, target);
      await tester.tap(target);
      await tester.pumpAndSettle();
      expect(taps, 1);
      expect(background.offset, 0);
      expect(dialog.offset, greaterThan(0));
      await tester.tap(find.text('关闭'));
      await tester.pumpAndSettle();
      await tester.pumpWidget(const SizedBox());
      background.dispose(); dialog.dispose();
    });
  }
}
