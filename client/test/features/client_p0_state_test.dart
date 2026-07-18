import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/files/file_models.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/files/files_page.dart';
import 'package:mnemonas_client/features/home/home_page.dart';

void main() {
  testWidgets(
    'home identifies stale cached data without claiming connectivity',
    (WidgetTester tester) async {
      await tester.pumpWidget(
        _app(
          HomePage(
            viewModel: HomeViewModel.ready(
              const HomeSummary(
                deviceName: '家庭存储',
                serverAddress: 'https://nas.example.com',
                connectionStatus: NasConnectionStatus.stale,
                storage: null,
              ),
            ),
            onRefresh: () async {},
            onOpenFiles: () {},
          ),
        ),
      );

      expect(find.textContaining('数据可能已过期'), findsOneWidget);
      expect(find.text('需刷新'), findsOneWidget);
      expect(find.text('设备连接正常'), findsNothing);
    },
  );

  testWidgets(
    'files blocks write controls during a mutation but keeps cancel selection',
    (WidgetTester tester) async {
      final List<FileActionRequest> actions = <FileActionRequest>[];

      Widget files({required bool isMutating}) {
        return FilesPage(
          viewModel: FilesViewModel.ready(
            path: '/',
            entries: <FileEntry>[_entry(name: '说明.pdf', path: '/说明.pdf')],
            canWrite: true,
            isMutating: isMutating,
          ),
          onRefresh: () async {},
          onNavigatePath: (_) {},
          onOpenFile: (_) {},
          onAddAction: (_) {},
          onFileAction: actions.add,
        );
      }

      await tester.pumpWidget(_app(files(isMutating: false)));
      await tester.longPress(find.byKey(const Key('file-item-/说明.pdf')));
      await tester.pump();
      expect(find.text('已选择 1 项'), findsOneWidget);

      await tester.pumpWidget(_app(files(isMutating: true)));
      await tester.pump();

      expect(find.byType(LinearProgressIndicator), findsOneWidget);

      final IconButton clearSelection = tester.widget<IconButton>(
        find.byKey(const Key('files-clear-selection')),
      );
      final IconButton moveSelection = tester.widget<IconButton>(
        find.byKey(const Key('files-move-selection')),
      );
      final IconButton deleteSelection = tester.widget<IconButton>(
        find.byKey(const Key('files-delete-selection')),
      );
      expect(clearSelection.onPressed, isNotNull);
      expect(moveSelection.onPressed, isNull);
      expect(deleteSelection.onPressed, isNull);

      await tester.tap(find.byKey(const Key('files-delete-selection')));
      await tester.tap(find.byKey(const Key('files-move-selection')));
      expect(actions, isEmpty);

      await tester.tap(find.byKey(const Key('files-clear-selection')));
      await tester.pump();
      expect(find.text('已选择 1 项'), findsNothing);

      final PopupMenuButton<FilesAddAction> addMenu = tester
          .widget<PopupMenuButton<FilesAddAction>>(
            find.byKey(const Key('files-add-menu')),
          );
      expect(addMenu.enabled, isFalse);
    },
  );

  testWidgets(
    'files exposes stale cached data but blocks mutations until refresh',
    (WidgetTester tester) async {
      var refreshCalls = 0;
      final actions = <FileActionRequest>[];

      await tester.pumpWidget(
        _app(
          FilesPage(
            viewModel: FilesViewModel.ready(
              path: '/',
              entries: <FileEntry>[_entry(name: '说明.pdf', path: '/说明.pdf')],
              canWrite: true,
              mutationsEnabled: false,
              staleMessage: '目录刷新失败，当前显示的是上一次成功加载的内容。',
            ),
            onRefresh: () async {
              refreshCalls++;
            },
            onNavigatePath: (_) {},
            onOpenFile: (_) {},
            onAddAction: (_) {},
            onFileAction: actions.add,
          ),
        ),
      );

      expect(find.byKey(const Key('files-stale-notice')), findsOneWidget);
      final addMenu = tester.widget<PopupMenuButton<FilesAddAction>>(
        find.byKey(const Key('files-add-menu')),
      );
      expect(addMenu.enabled, isFalse);

      final fileMenu = find.descendant(
        of: find.byKey(const Key('file-item-/说明.pdf')),
        matching: find.byType(PopupMenuButton<FileItemAction>),
      );
      await tester.tap(fileMenu);
      await tester.pumpAndSettle();
      expect(find.text('下载'), findsOneWidget);
      expect(find.text('详细信息'), findsOneWidget);
      expect(find.text('重命名'), findsNothing);
      expect(find.text('移动'), findsNothing);
      expect(find.text('复制'), findsNothing);
      expect(find.text('删除'), findsNothing);
      await tester.tapAt(Offset.zero);
      await tester.pumpAndSettle();

      await tester.longPress(find.byKey(const Key('file-item-/说明.pdf')));
      await tester.pump();
      final moveSelection = tester.widget<IconButton>(
        find.byKey(const Key('files-move-selection')),
      );
      final deleteSelection = tester.widget<IconButton>(
        find.byKey(const Key('files-delete-selection')),
      );
      expect(moveSelection.onPressed, isNull);
      expect(deleteSelection.onPressed, isNull);

      await tester.tap(find.text('刷新目录'));
      await tester.pump();
      expect(refreshCalls, 1);
      expect(actions, isEmpty);
    },
  );
}

FileEntry _entry({
  required String name,
  required String path,
  bool isDirectory = false,
}) {
  return FileEntry(
    name: name,
    path: path,
    isDirectory: isDirectory,
    size: isDirectory ? 0 : 2048,
    modifiedAt: DateTime.utc(2026, 7, 19, 10),
    capabilities: const FileCapabilities(
      read: true,
      concreteRead: true,
      write: true,
    ),
  );
}

Widget _app(Widget child) {
  return MaterialApp(
    theme: MnemoTheme.light,
    home: Scaffold(body: child),
  );
}
