import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/files/file_models.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/versions/version_history_sheet.dart';

void main() {
  testWidgets('moves from loading to an honest current-only state', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(390, 844));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final load = Completer<FileVersionHistory>();

    await tester.pumpWidget(
      _app(
        VersionHistorySheet(
          entry: _entry(),
          onLoad: () => load.future,
          onPreview: (_) async {},
          onDownload: (_) async {},
          onClose: () {},
        ),
      ),
    );

    expect(find.byKey(const Key('version-history-loading')), findsOneWidget);

    load.complete(_history());
    await tester.pump();
    await tester.pump();

    expect(
      find.byKey(const Key('version-history-current-only')),
      findsOneWidget,
    );
    expect(find.text('尚无历史版本'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp(r'^状态：当前版本')), findsOneWidget);
    expect(find.text('/文档/家庭 记录.txt'), findsOneWidget);
    expect(find.text('预览'), findsNothing);
    expect(find.text('下载'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('shows error recovery and fences repeated version actions', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(390, 844));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    var loadCalls = 0;
    var previewCalls = 0;
    final preview = Completer<void>();

    await tester.pumpWidget(
      _app(
        VersionHistorySheet(
          entry: _entry(),
          onLoad: () async {
            loadCalls++;
            if (loadCalls == 1) {
              throw StateError('offline');
            }
            return _history(withHistoricalVersion: true);
          },
          onPreview: (_) {
            previewCalls++;
            return preview.future;
          },
          onDownload: (_) async {},
          onClose: () {},
        ),
      ),
    );
    await tester.pump();
    await tester.pump();

    expect(find.byKey(const Key('version-history-error')), findsOneWidget);
    await tester.tap(find.text('重试'));
    await tester.pump();
    await tester.pump();

    expect(find.byKey(const Key('version-history-list')), findsOneWidget);
    final historicalHash = _hash('b');
    final previewButton = find.byKey(Key('version-preview-2-$historicalHash'));
    expect(previewButton, findsOneWidget);
    expect(
      find.byKey(Key('version-download-2-$historicalHash')),
      findsOneWidget,
    );
    expect(find.byKey(Key('version-restore-2-$historicalHash')), findsNothing);

    await tester.tap(previewButton);
    await tester.tap(previewButton);
    await tester.pump();

    expect(previewCalls, 1);
    expect(find.bySemanticsLabel(RegExp('正在预览版本')), findsOneWidget);

    preview.complete();
    await tester.pump();
    await tester.pump();
    expect(previewCalls, 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('disables version actions while history refresh is pending', (
    tester,
  ) async {
    final refresh = Completer<FileVersionHistory>();
    var loadCalls = 0;
    final historicalHash = _hash('b');

    await tester.pumpWidget(
      _app(
        VersionHistorySheet(
          entry: _entry(),
          onLoad: () {
            loadCalls++;
            return loadCalls == 1
                ? Future<FileVersionHistory>.value(
                    _history(withHistoricalVersion: true),
                  )
                : refresh.future;
          },
          onPreview: (_) async {},
          onDownload: (_) async {},
          onRestore: (_, _) async =>
              _historyAfterRestore(currentCharacter: 'b'),
          onClose: () {},
        ),
      ),
    );
    await tester.pump();
    await tester.pump();

    await tester.tap(find.byKey(const Key('version-history-refresh')));
    await tester.pump();

    final preview = tester.widget<OutlinedButton>(
      find.byKey(Key('version-preview-2-$historicalHash')),
    );
    final download = tester.widget<OutlinedButton>(
      find.byKey(Key('version-download-2-$historicalHash')),
    );
    final restore = tester.widget<FilledButton>(
      find.byKey(Key('version-restore-2-$historicalHash')),
    );
    expect(preview.onPressed, isNull);
    expect(download.onPressed, isNull);
    expect(restore.onPressed, isNull);

    refresh.complete(_history(withHistoricalVersion: true));
    await tester.pump();
    await tester.pump();
    expect(
      tester
          .widget<OutlinedButton>(
            find.byKey(Key('version-preview-2-$historicalHash')),
          )
          .onPressed,
      isNotNull,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'confirms one administrator restore with complete impact context',
    (tester) async {
      await tester.binding.setSurfaceSize(const Size(390, 844));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      var restoreCalls = 0;
      String? expectedCurrentHash;
      final restore = Completer<FileVersionHistory>();
      final historicalHash = _hash('b');

      await tester.pumpWidget(
        _app(
          VersionHistorySheet(
            entry: _entry(),
            onLoad: () async => _history(withHistoricalVersion: true),
            onPreview: (_) async {},
            onDownload: (_) async {},
            onRestore: (_, expectedHash) {
              restoreCalls++;
              expectedCurrentHash = expectedHash;
              return restore.future;
            },
            onClose: () {},
          ),
        ),
      );
      await tester.pump();
      await tester.pump();

      await tester.tap(find.byKey(Key('version-restore-2-$historicalHash')));
      await tester.pumpAndSettle();

      expect(find.text('恢复此历史版本？'), findsOneWidget);
      expect(find.text('/文档/家庭 记录.txt'), findsWidgets);
      expect(
        find.descendant(
          of: find.byType(AlertDialog),
          matching: find.text('4.0 KB'),
        ),
        findsOneWidget,
      );
      expect(find.text('bbbbbbbbbbbb…'), findsWidgets);
      expect(find.textContaining('服务端会先把当前内容保存为新的安全版本'), findsOneWidget);

      await tester.tap(find.byKey(const Key('version-restore-confirm')));
      await tester.pump();

      expect(restoreCalls, 1);
      expect(expectedCurrentHash, _hash('a'));
      expect(find.byKey(const Key('version-restore-confirm')), findsNothing);
      expect(find.bySemanticsLabel(RegExp('正在恢复历史版本')), findsOneWidget);

      restore.complete(_historyAfterRestore(currentCharacter: 'c'));
      await tester.pumpAndSettle();

      expect(restoreCalls, 1);
      expect(find.text('恢复此历史版本？'), findsNothing);
      expect(find.text('所选版本已恢复，但文件随后再次发生变化，当前历史已刷新。'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('does not offer a blind retry after a restore failure', (
    tester,
  ) async {
    var restoreCalls = 0;
    final historicalHash = _hash('b');

    await tester.pumpWidget(
      _app(
        VersionHistorySheet(
          entry: _entry(),
          onLoad: () async => _history(withHistoricalVersion: true),
          onPreview: (_) async {},
          onDownload: (_) async {},
          onRestore: (_, _) async {
            restoreCalls++;
            throw StateError('result unknown');
          },
          onClose: () {},
        ),
      ),
    );
    await tester.pump();
    await tester.pump();

    await tester.tap(find.byKey(Key('version-restore-2-$historicalHash')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('version-restore-confirm')));
    await tester.pump();
    await tester.pump();

    expect(restoreCalls, 1);
    expect(find.text('需要核对恢复结果'), findsOneWidget);
    expect(find.byKey(const Key('version-restore-confirm')), findsNothing);
    expect(find.text('关闭'), findsOneWidget);
    expect(find.textContaining('请关闭并刷新目录后重新打开版本历史'), findsOneWidget);

    await tester.tap(find.byKey(const Key('version-restore-cancel')));
    await tester.pumpAndSettle();

    expect(find.text('恢复此历史版本？'), findsNothing);
    expect(find.byKey(const Key('version-restore-blocked')), findsOneWidget);
    expect(find.byKey(Key('version-restore-2-$historicalHash')), findsNothing);
    expect(
      find.descendant(
        of: find.byKey(const Key('version-restore-blocked')),
        matching: find.textContaining('请关闭并刷新目录后重新打开版本历史'),
      ),
      findsOneWidget,
    );
    expect(restoreCalls, 1);
  });

  testWidgets('does not mark a definite restore rejection as unconfirmed', (
    tester,
  ) async {
    final historicalHash = _hash('b');

    await tester.pumpWidget(
      _app(
        VersionHistorySheet(
          entry: _entry(),
          onLoad: () async => _history(withHistoricalVersion: true),
          onPreview: (_) async {},
          onDownload: (_) async {},
          onRestore: (_, _) async {
            throw const ApiException(
              kind: ApiFailureKind.local,
              code: 'VERSION_TARGET_CHANGED',
              message: 'file changed before restore',
            );
          },
          errorMessageBuilder: (_) => '当前文件已经发生变化。',
          onClose: () {},
        ),
      ),
    );
    await tester.pump();
    await tester.pump();

    await tester.tap(find.byKey(Key('version-restore-2-$historicalHash')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('version-restore-confirm')));
    await tester.pump();
    await tester.pump();

    expect(find.textContaining('本次恢复未完成'), findsOneWidget);
    expect(find.textContaining('避免重复提交'), findsNothing);
    await tester.tap(find.byKey(const Key('version-restore-cancel')));
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('version-restore-blocked')), findsNothing);
    expect(
      find.byKey(Key('version-restore-2-$historicalHash')),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'uses sequence keys and hides restore for duplicate current hash',
    (tester) async {
      final duplicateHash = _hash('a');

      await tester.pumpWidget(
        _app(
          VersionHistorySheet(
            entry: _entry(),
            onLoad: () async => _historyWithDuplicateCurrentHash(),
            onPreview: (_) async {},
            onDownload: (_) async {},
            onRestore: (_, _) async => _historyWithDuplicateCurrentHash(),
            onClose: () {},
          ),
        ),
      );
      await tester.pump();
      await tester.pump();

      expect(
        find.byKey(Key('version-history-item-1-$duplicateHash')),
        findsOneWidget,
      );
      expect(
        find.byKey(Key('version-history-item-2-$duplicateHash')),
        findsOneWidget,
      );
      expect(
        find.byKey(Key('version-preview-2-$duplicateHash')),
        findsOneWidget,
      );
      expect(
        find.byKey(Key('version-download-2-$duplicateHash')),
        findsOneWidget,
      );
      expect(find.byKey(Key('version-restore-2-$duplicateHash')), findsNothing);
      expect(tester.takeException(), isNull);
    },
  );
}

FileEntry _entry() {
  return FileEntry(
    name: '家庭 记录.txt',
    path: '/文档/家庭 记录.txt',
    isDirectory: false,
    size: 8192,
    modifiedAt: DateTime.utc(2026, 7, 19, 8),
    capabilities: const FileCapabilities(
      read: true,
      concreteRead: true,
      write: true,
    ),
    contentHash: _hash('a'),
    versioned: true,
  );
}

FileVersionHistory _history({bool withHistoricalVersion = false}) {
  return FileVersionHistory.fromJson(<String, Object?>{
    'path': '/文档/家庭 记录.txt',
    'versions': <Object?>[
      <String, Object?>{
        'version': 1,
        'hash': _hash('a'),
        'size': 8192,
        'timestamp': '2026-07-19T08:00:00Z',
        'comment': '(current)',
      },
      if (withHistoricalVersion)
        <String, Object?>{
          'version': 2,
          'hash': _hash('b'),
          'size': 4096,
          'timestamp': '2026-07-18T08:00:00Z',
          'comment': 'before restore',
        },
    ],
  }, expectedPath: '/文档/家庭 记录.txt');
}

FileVersionHistory _historyAfterRestore({required String currentCharacter}) {
  return FileVersionHistory.fromJson(<String, Object?>{
    'path': '/文档/家庭 记录.txt',
    'versions': <Object?>[
      <String, Object?>{
        'version': 1,
        'hash': _hash(currentCharacter),
        'size': 4096,
        'timestamp': '2026-07-19T09:00:00Z',
        'comment': '(current)',
      },
      <String, Object?>{
        'version': 2,
        'hash': _hash('b'),
        'size': 4096,
        'timestamp': '2026-07-18T08:00:00Z',
        'comment': 'restored source',
      },
    ],
  }, expectedPath: '/文档/家庭 记录.txt');
}

FileVersionHistory _historyWithDuplicateCurrentHash() {
  return FileVersionHistory.fromJson(<String, Object?>{
    'path': '/文档/家庭 记录.txt',
    'versions': <Object?>[
      <String, Object?>{
        'version': 1,
        'hash': _hash('a'),
        'size': 8192,
        'timestamp': '2026-07-19T08:00:00Z',
        'comment': '(current)',
      },
      <String, Object?>{
        'version': 2,
        'hash': _hash('a'),
        'size': 8192,
        'timestamp': '2026-07-18T08:00:00Z',
        'comment': 'before restore',
      },
    ],
  }, expectedPath: '/文档/家庭 记录.txt');
}

String _hash(String character) => List.filled(64, character).join();

Widget _app(Widget child) {
  return MaterialApp(
    theme: MnemoTheme.light,
    home: Scaffold(body: child),
  );
}
