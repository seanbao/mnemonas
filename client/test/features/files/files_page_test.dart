import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/files/file_models.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/files/files_page.dart';

void main() {
  testWidgets('opens folders and enters multi-selection on long press', (
    WidgetTester tester,
  ) async {
    String? navigatedPath;
    final List<FileActionRequest> actions = <FileActionRequest>[];
    await tester.pumpWidget(
      _app(
        FilesPage(
          viewModel: FilesViewModel.ready(
            path: '/',
            entries: <FileEntry>[
              _entry(name: '照片', path: '/照片', isDirectory: true),
              _entry(name: '说明.pdf', path: '/说明.pdf'),
            ],
            canWrite: true,
          ),
          onRefresh: () async {},
          onNavigatePath: (String path) => navigatedPath = path,
          onOpenFile: (_) {},
          onAddAction: (_) {},
          onFileAction: actions.add,
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('file-item-/照片')));
    expect(navigatedPath, '/照片');

    await tester.longPress(find.byKey(const Key('file-item-/说明.pdf')));
    await tester.pump();
    expect(find.text('已选择 1 项'), findsOneWidget);

    await tester.tap(find.byKey(const Key('file-item-/照片')));
    await tester.pump();
    expect(find.text('已选择 2 项'), findsOneWidget);

    await tester.tap(find.byKey(const Key('files-delete-selection')));
    expect(actions.single.action, FileItemAction.delete);
    expect(actions.single.entries.map((FileEntry item) => item.path), {
      '/照片',
      '/说明.pdf',
    });
  });

  testWidgets('switches between list and grid and exposes add actions', (
    WidgetTester tester,
  ) async {
    FilesAddAction? addAction;
    await tester.pumpWidget(
      _app(
        FilesPage(
          viewModel: FilesViewModel.ready(
            path: '/文档',
            entries: <FileEntry>[_entry(name: '记录.txt', path: '/文档/记录.txt')],
            canWrite: true,
          ),
          onRefresh: () async {},
          onNavigatePath: (_) {},
          onOpenFile: (_) {},
          onAddAction: (FilesAddAction action) => addAction = action,
          onFileAction: (_) {},
        ),
      ),
    );

    expect(find.byKey(const Key('files-list')), findsOneWidget);
    await tester.tap(find.byKey(const Key('files-display-mode')));
    await tester.pump();
    expect(find.byKey(const Key('files-grid')), findsOneWidget);

    await tester.tap(find.byKey(const Key('files-add-menu')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('上传文件'));
    await tester.pumpAndSettle();
    expect(addAction, FilesAddAction.uploadFiles);
  });

  testWidgets('shows an honest empty state for a writable directory', (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(
      _app(
        FilesPage(
          viewModel: const FilesViewModel.ready(
            path: '/',
            entries: <FileEntry>[],
            canWrite: true,
          ),
          onRefresh: () async {},
          onNavigatePath: (_) {},
          onOpenFile: (_) {},
          onAddAction: (_) {},
          onFileAction: (_) {},
        ),
      ),
    );

    expect(find.text('这里还没有文件'), findsOneWidget);
    expect(find.text('上传文件'), findsWidgets);
    expect(find.text('新建文件夹'), findsWidgets);
  });

  testWidgets(
    'offers downloads and version history without a directory-list hash',
    (WidgetTester tester) async {
      final actions = <FileActionRequest>[];
      await tester.pumpWidget(
        _app(
          FilesPage(
            viewModel: FilesViewModel.ready(
              path: '/文档',
              entries: <FileEntry>[
                _entry(name: '记录.txt', path: '/文档/记录.txt', versioned: true),
              ],
              canWrite: true,
            ),
            onRefresh: () async {},
            onNavigatePath: (_) {},
            onOpenFile: (_) {},
            onAddAction: (_) {},
            onFileAction: actions.add,
          ),
        ),
      );

      expect(
        _entry(name: '记录.txt', path: '/文档/记录.txt', versioned: true).contentHash,
        isNull,
      );
      final fileItem = find.byKey(const Key('file-item-/文档/记录.txt'));
      final menu = find.descendant(
        of: fileItem,
        matching: find.byType(PopupMenuButton<FileItemAction>),
      );
      await tester.tap(menu);
      await tester.pumpAndSettle();

      expect(find.text('下载'), findsOneWidget);
      expect(find.text('版本历史'), findsOneWidget);
      await tester.tap(find.text('版本历史'));
      await tester.pumpAndSettle();

      expect(actions.single.action, FileItemAction.versions);
      expect(actions.single.entries.single.path, '/文档/记录.txt');
    },
  );

  testWidgets('does not offer history for a file outside version policy', (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(
      _app(
        FilesPage(
          viewModel: FilesViewModel.ready(
            path: '/媒体',
            entries: <FileEntry>[
              _entry(name: 'archive.raw', path: '/媒体/archive.raw'),
            ],
            canWrite: true,
          ),
          onRefresh: () async {},
          onNavigatePath: (_) {},
          onOpenFile: (_) {},
          onAddAction: (_) {},
          onFileAction: (_) {},
        ),
      ),
    );

    final menu = find.descendant(
      of: find.byKey(const Key('file-item-/媒体/archive.raw')),
      matching: find.byType(PopupMenuButton<FileItemAction>),
    );
    await tester.tap(menu);
    await tester.pumpAndSettle();

    expect(find.text('下载'), findsOneWidget);
    expect(find.text('版本历史'), findsNothing);
  });

  testWidgets(
    'system back clears selection before navigating to the parent directory',
    (WidgetTester tester) async {
      final List<String> navigatedPaths = <String>[];
      await tester.pumpWidget(
        _app(
          FilesPage(
            viewModel: FilesViewModel.ready(
              path: '/文档/项目',
              entries: <FileEntry>[
                _entry(name: '记录.txt', path: '/文档/项目/记录.txt'),
              ],
              canWrite: true,
            ),
            onRefresh: () async {},
            onNavigatePath: navigatedPaths.add,
            onOpenFile: (_) {},
            onAddAction: (_) {},
            onFileAction: (_) {},
          ),
        ),
      );

      await tester.longPress(find.byKey(const Key('file-item-/文档/项目/记录.txt')));
      await tester.pump();
      expect(find.text('已选择 1 项'), findsOneWidget);

      await tester.binding.handlePopRoute();
      await tester.pump();

      expect(find.text('已选择 1 项'), findsNothing);
      expect(navigatedPaths, isEmpty);

      await tester.binding.handlePopRoute();
      await tester.pump();

      expect(navigatedPaths, <String>['/文档']);
    },
  );

  testWidgets('system back navigates a top-level directory to root', (
    WidgetTester tester,
  ) async {
    final List<String> navigatedPaths = <String>[];
    await tester.pumpWidget(
      _app(
        FilesPage(
          viewModel: const FilesViewModel.ready(
            path: '/文档',
            entries: <FileEntry>[],
            canWrite: true,
          ),
          onRefresh: () async {},
          onNavigatePath: navigatedPaths.add,
          onOpenFile: (_) {},
          onAddAction: (_) {},
          onFileAction: (_) {},
        ),
      ),
    );

    await tester.binding.handlePopRoute();
    await tester.pump();

    expect(navigatedPaths, <String>['/']);
  });

  testWidgets('system back follows consecutive directory state updates', (
    WidgetTester tester,
  ) async {
    String path = '/文档/项目';
    final List<String> navigatedPaths = <String>[];
    await tester.pumpWidget(
      _app(
        StatefulBuilder(
          builder: (BuildContext context, StateSetter setState) {
            return FilesPage(
              viewModel: FilesViewModel.ready(
                path: path,
                entries: const <FileEntry>[],
                canWrite: true,
              ),
              onRefresh: () async {},
              onNavigatePath: (String nextPath) {
                navigatedPaths.add(nextPath);
                setState(() => path = nextPath);
              },
              onOpenFile: (_) {},
              onAddAction: (_) {},
              onFileAction: (_) {},
            );
          },
        ),
      ),
    );

    expect(
      tester
          .widget<PopScope<Object?>>(find.byKey(const Key('files-back-scope')))
          .canPop,
      isFalse,
    );

    await tester.binding.handlePopRoute();
    await tester.pump();
    await tester.binding.handlePopRoute();
    await tester.pump();

    expect(navigatedPaths, <String>['/文档', '/']);
    expect(path, '/');
    expect(
      tester
          .widget<PopScope<Object?>>(find.byKey(const Key('files-back-scope')))
          .canPop,
      isTrue,
    );
  });

  testWidgets('root directory allows system back to leave the route', (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(
      _app(
        FilesPage(
          viewModel: const FilesViewModel.ready(
            path: '/',
            entries: <FileEntry>[],
            canWrite: true,
          ),
          onRefresh: () async {},
          onNavigatePath: (_) {},
          onOpenFile: (_) {},
          onAddAction: (_) {},
          onFileAction: (_) {},
        ),
      ),
    );

    final PopScope<Object?> scope = tester.widget<PopScope<Object?>>(
      find.byKey(const Key('files-back-scope')),
    );
    expect(scope.canPop, isTrue);
  });

  testWidgets('root directory clears selection before allowing route exit', (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(
      _app(
        FilesPage(
          viewModel: FilesViewModel.ready(
            path: '/',
            entries: <FileEntry>[
              _entry(name: '记录.txt', path: '/记录.txt'),
            ],
            canWrite: true,
          ),
          onRefresh: () async {},
          onNavigatePath: (_) {},
          onOpenFile: (_) {},
          onAddAction: (_) {},
          onFileAction: (_) {},
        ),
      ),
    );

    await tester.longPress(find.byKey(const Key('file-item-/记录.txt')));
    await tester.pump();
    expect(find.text('已选择 1 项'), findsOneWidget);
    expect(
      tester
          .widget<PopScope<Object?>>(find.byKey(const Key('files-back-scope')))
          .canPop,
      isFalse,
    );

    await tester.binding.handlePopRoute();
    await tester.pump();

    expect(find.text('已选择 1 项'), findsNothing);
    expect(
      tester
          .widget<PopScope<Object?>>(find.byKey(const Key('files-back-scope')))
          .canPop,
      isTrue,
    );
  });
}

FileEntry _entry({
  required String name,
  required String path,
  bool isDirectory = false,
  bool versioned = false,
}) {
  return FileEntry(
    name: name,
    path: path,
    isDirectory: isDirectory,
    size: isDirectory ? 0 : 2048,
    modifiedAt: DateTime.utc(2026, 7, 19, 10),
    versioned: versioned,
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
