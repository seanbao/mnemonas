import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/trash/trash_models.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/trash/trash_page.dart';

void main() {
  testWidgets('renders loading, error, and empty states with refresh', (
    tester,
  ) async {
    var refreshCount = 0;
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: const TrashViewModel.loading(),
          onRefresh: () async => refreshCount++,
          onRestore: _restored,
          onDeletePermanently: _deleted,
        ),
      ),
    );
    expect(find.byKey(const Key('trash-loading')), findsOneWidget);

    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: const TrashViewModel.error('设备暂时不可用'),
          onRefresh: () async => refreshCount++,
          onRestore: _restored,
          onDeletePermanently: _deleted,
        ),
      ),
    );
    expect(find.byKey(const Key('trash-error')), findsOneWidget);
    expect(find.text('设备暂时不可用'), findsOneWidget);
    await tester.tap(find.text('重试'));
    await tester.pump();
    expect(refreshCount, 1);

    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(
            listing: _listing(items: const []),
            canWrite: true,
          ),
          onRefresh: () async => refreshCount++,
          onRestore: _restored,
          onDeletePermanently: _deleted,
        ),
      ),
    );
    expect(find.byKey(const Key('trash-empty')), findsOneWidget);
    expect(find.text('回收站为空'), findsOneWidget);
    await tester.drag(
      find.byKey(const Key('trash-empty')),
      const Offset(0, 300),
    );
    await tester.pumpAndSettle();
    expect(refreshCount, 2);
  });

  testWidgets('shows policy and complete item metadata on a narrow screen', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(390, 844));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: true),
          onRefresh: () async {},
          onRestore: _restored,
          onDeletePermanently: _deleted,
        ),
      ),
    );

    expect(find.byKey(const Key('trash-ready')), findsOneWidget);
    expect(find.byKey(const Key('trash-list')), findsOneWidget);
    expect(find.byKey(const Key('trash-policy-summary')), findsOneWidget);
    expect(find.text('2 项'), findsOneWidget);
    expect(find.text('新删除进入回收站'), findsOneWidget);
    expect(find.text('/docs/report.txt'), findsOneWidget);
    expect(find.text('含历史版本'), findsOneWidget);
    expect(find.text('文件'), findsOneWidget);
    expect(find.textContaining('删除于'), findsWidgets);
    expect(find.textContaining('到期于'), findsWidgets);

    await tester.scrollUntilVisible(
      find.text('/photos/家庭相册'),
      250,
      scrollable: find.byType(Scrollable).first,
    );
    expect(find.text('/photos/家庭相册'), findsOneWidget);
    expect(find.text('文件夹'), findsOneWidget);
  });

  testWidgets('marks cached trash as stale when refresh fails', (tester) async {
    var refreshCount = 0;
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(
            listing: _listing(),
            canWrite: false,
            refreshErrorMessage: '设备暂时不可用。',
          ),
          onRefresh: () async => refreshCount++,
          onRestore: _restored,
          onDeletePermanently: _deleted,
        ),
      ),
    );

    expect(find.byKey(const Key('trash-refresh-error')), findsOneWidget);
    expect(find.textContaining('当前显示上一次成功加载的数据'), findsOneWidget);
    expect(find.byKey(const Key('trash-restore-item-a')), findsNothing);
    expect(find.byKey(const Key('trash-delete-item-a')), findsNothing);
    await tester.tap(find.text('重试'));
    await tester.pump();
    expect(refreshCount, 1);
  });

  testWidgets('uses a multi-column layout on a wide screen', (tester) async {
    await tester.binding.setSurfaceSize(const Size(1280, 900));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: true),
          onRefresh: () async {},
          onRestore: _restored,
          onDeletePermanently: _deleted,
        ),
      ),
    );

    expect(find.byKey(const Key('trash-grid')), findsOneWidget);
    expect(find.byKey(const Key('trash-list')), findsNothing);
    expect(find.byKey(const Key('trash-item-item-a')), findsOneWidget);
    expect(find.byKey(const Key('trash-item-item-b')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('guest mode remains refreshable and read-only', (tester) async {
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: false),
          onRefresh: () async {},
          onRestore: _restored,
          onDeletePermanently: _deleted,
        ),
      ),
    );

    expect(find.text('只读访问'), findsOneWidget);
    expect(find.byKey(const Key('trash-select-item-a')), findsNothing);
    expect(find.byKey(const Key('trash-restore-item-a')), findsNothing);
    expect(find.byKey(const Key('trash-delete-item-a')), findsNothing);
    expect(find.byType(RefreshIndicator), findsOneWidget);
  });

  testWidgets('retries a restore conflict with a normalized custom path', (
    tester,
  ) async {
    final destinations = <String?>[];
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: true),
          onRefresh: () async {},
          onRestore: (item, destinationPath) async {
            destinations.add(destinationPath);
            return destinationPath == null
                ? TrashRestoreOutcome.pathConflict
                : TrashRestoreOutcome.restored;
          },
          onDeletePermanently: _deleted,
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('trash-restore-item-a')));
    await tester.pumpAndSettle();
    expect(find.text('选择新的恢复位置'), findsOneWidget);

    await tester.enterText(
      find.byKey(const Key('trash-restore-custom-path')),
      'restored//report.txt',
    );
    await tester.tap(find.byKey(const Key('trash-restore-custom-submit')));
    await tester.pumpAndSettle();

    expect(destinations, <String?>[null, '/restored/report.txt']);
    expect(find.text('选择新的恢复位置'), findsNothing);
    expect(find.text('“report.txt”已恢复。'), findsOneWidget);
  });

  testWidgets('keeps a repeated custom-path conflict in the retry dialog', (
    tester,
  ) async {
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: true),
          onRefresh: () async {},
          onRestore: (item, destinationPath) async =>
              TrashRestoreOutcome.pathConflict,
          onDeletePermanently: _deleted,
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('trash-restore-item-a')));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const Key('trash-restore-custom-path')),
      '/restored/report.txt',
    );
    await tester.tap(find.byKey(const Key('trash-restore-custom-submit')));
    await tester.pumpAndSettle();

    expect(find.text('目标路径已存在，请更换文件名或目录。'), findsOneWidget);
    expect(find.text('选择新的恢复位置'), findsOneWidget);
  });

  testWidgets('freezes a multi-selection before permanent deletion', (
    tester,
  ) async {
    TrashSelectionSnapshot? submitted;
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: true),
          onRefresh: () async {},
          onRestore: _restored,
          onDeletePermanently: (selection) async {
            submitted = selection;
            return TrashDeleteOutcome.allDeleted(selection);
          },
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('trash-select-item-a')));
    await tester.tap(find.byKey(const Key('trash-select-item-b')));
    await tester.pump();
    expect(find.text('已选择 2 项'), findsOneWidget);

    await tester.tap(find.byKey(const Key('trash-delete-selection')));
    await tester.pumpAndSettle();
    expect(find.text('2 项、42 B 将被永久删除，且无法恢复。'), findsOneWidget);

    final confirm = find.byKey(const Key('trash-delete-confirm'));
    final confirmSize = tester.getSize(confirm);
    expect(confirmSize.width, greaterThanOrEqualTo(48));
    expect(confirmSize.height, greaterThanOrEqualTo(48));
    await tester.tap(confirm);
    await tester.pumpAndSettle();

    expect(submitted?.ids, ['item-a', 'item-b']);
    expect(find.text('已永久删除 2 项。'), findsOneWidget);
  });

  testWidgets('caps selection before the server batch limit is exceeded', (
    tester,
  ) async {
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: true),
          onRefresh: () async {},
          onRestore: _restored,
          onDeletePermanently: _deleted,
          selectionLimit: 1,
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('trash-select-item-a')));
    await tester.tap(find.byKey(const Key('trash-select-item-b')));
    await tester.pump();

    expect(find.text('已选择 1 项'), findsOneWidget);
    expect(find.text('每次最多选择 1 项，请分批永久删除。'), findsOneWidget);
  });

  testWidgets('keeps partial deletion failures selected for retry', (
    tester,
  ) async {
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: true),
          onRefresh: () async {},
          onRestore: _restored,
          onDeletePermanently: (selection) async => TrashDeleteOutcome(
            deletedIds: <String>[selection.ids.first],
            skippedIds: const <String>[],
            remainingIds: <String>[selection.ids.last],
          ),
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('trash-select-item-a')));
    await tester.tap(find.byKey(const Key('trash-select-item-b')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('trash-delete-selection')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('trash-delete-confirm')));
    await tester.pumpAndSettle();

    expect(find.text('已选择 1 项'), findsOneWidget);
    expect(find.text('已永久删除 1 项，1 项仍待处理。'), findsOneWidget);
  });

  testWidgets('removes skipped items from the frozen selection', (
    tester,
  ) async {
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: true),
          onRefresh: () async {},
          onRestore: _restored,
          onDeletePermanently: (selection) async => TrashDeleteOutcome(
            deletedIds: const <String>[],
            skippedIds: selection.ids,
            remainingIds: const <String>[],
          ),
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('trash-select-item-a')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('trash-delete-selection')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('trash-delete-confirm')));
    await tester.pumpAndSettle();

    expect(find.textContaining('已选择'), findsNothing);
    expect(find.text('已永久删除 0 项，1 项已不存在。'), findsOneWidget);
  });

  testWidgets('destructive item controls expose 48dp touch targets', (
    tester,
  ) async {
    await tester.pumpWidget(
      _app(
        TrashPage(
          viewModel: TrashViewModel.ready(listing: _listing(), canWrite: true),
          onRefresh: () async {},
          onRestore: _restored,
          onDeletePermanently: _deleted,
        ),
      ),
    );

    final deleteButton = find.byKey(const Key('trash-delete-item-a'));
    final size = tester.getSize(deleteButton);
    expect(size.width, greaterThanOrEqualTo(48));
    expect(size.height, greaterThanOrEqualTo(48));
  });
}

Future<TrashRestoreOutcome> _restored(
  TrashItem item,
  String? destinationPath,
) async {
  return TrashRestoreOutcome.restored;
}

Future<TrashDeleteOutcome> _deleted(TrashSelectionSnapshot selection) async {
  return TrashDeleteOutcome.allDeleted(selection);
}

TrashListing _listing({List<Map<String, dynamic>>? items}) {
  final values =
      items ??
      <Map<String, dynamic>>[
        {
          'id': 'item-a',
          'originalPath': '/docs/report.txt',
          'deletedAt': '2026-07-19T12:00:00+08:00',
          'expiresAt': '2099-08-18T12:00:00+08:00',
          'name': 'report.txt',
          'isDir': false,
          'size': 42,
          'hadVersions': true,
        },
        {
          'id': 'item-b',
          'originalPath': '/photos/家庭相册',
          'deletedAt': '2026-07-19T12:30:00+08:00',
          'expiresAt': '2099-08-18T12:30:00+08:00',
          'name': '家庭相册',
          'isDir': true,
          'size': 0,
          'hadVersions': false,
        },
      ];
  return TrashListing.fromJson({
    'items': values,
    'count': values.length,
    'totalSize': values.fold<int>(
      0,
      (sum, item) => sum + (item['size']! as int),
    ),
    'retentionDays': 30,
    'retentionEnabled': true,
    'retentionMaxSize': 1024 * 1024 * 1024,
    'trashAutoCleanupEnabled': true,
  });
}

Widget _app(Widget child) {
  return MaterialApp(
    theme: MnemoTheme.light,
    home: Scaffold(body: child),
  );
}
