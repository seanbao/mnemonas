import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/app/client_state.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/transfers/transfer_center.dart';

void main() {
  testWidgets('groups transfers and exposes state-specific actions', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(800, 1200));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final paused = <String>[];
    final retried = <String>[];
    final deleted = <String>[];

    await tester.pumpWidget(
      _app(
        TransferCenter(
          transfers: <ClientTransfer>[
            _transfer(id: 'running', status: TransferStatus.running),
            _transfer(id: 'queued', status: TransferStatus.queued),
            _transfer(id: 'failed', status: TransferStatus.failed),
            _transfer(
              id: 'invalid',
              status: TransferStatus.failed,
              canRetry: false,
            ),
            _transfer(
              id: 'destination',
              status: TransferStatus.awaitingDestination,
            ),
            _transfer(id: 'completed', status: TransferStatus.completed),
          ],
          onPause: paused.add,
          onRetry: (id) async => retried.add(id),
          onDelete: (id) async => deleted.add(id),
        ),
      ),
    );

    expect(find.text('进行中 · 2'), findsOneWidget);
    expect(find.text('需要处理 · 3'), findsOneWidget);
    expect(find.byTooltip('暂停 running.txt'), findsOneWidget);
    expect(find.byKey(const Key('transfer-pause-queued')), findsNothing);
    expect(find.byTooltip('重试 failed.txt'), findsOneWidget);
    expect(find.byTooltip('重试 invalid.txt'), findsNothing);
    expect(find.byKey(const Key('transfer-more-invalid')), findsOneWidget);
    expect(find.byTooltip('选择保存位置 destination.txt'), findsOneWidget);

    await tester.tap(find.byTooltip('暂停 running.txt'));
    await tester.tap(find.byTooltip('重试 failed.txt'));
    await tester.drag(
      find.byKey(const Key('transfer-center-list')),
      const Offset(0, -600),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('选择保存位置 destination.txt'));
    await tester.scrollUntilVisible(
      find.byKey(const Key('transfer-section-recent')),
      240,
      scrollable: find.descendant(
        of: find.byKey(const Key('transfer-center-list')),
        matching: find.byType(Scrollable),
      ),
    );
    expect(find.text('最近记录 · 1'), findsOneWidget);
    expect(find.byTooltip('清除记录 completed.txt'), findsOneWidget);
    await tester.tap(find.byTooltip('清除记录 completed.txt'));
    await tester.pump();

    expect(paused, <String>['running']);
    expect(retried, <String>['failed', 'destination']);
    expect(deleted, <String>['completed']);
  });

  testWidgets('confirms destructive and unconfirmed-result removal', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(800, 1200));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final deleted = <String>[];

    await tester.pumpWidget(
      _app(
        TransferCenter(
          transfers: <ClientTransfer>[
            _transfer(id: 'paused', status: TransferStatus.paused),
            _transfer(
              id: 'unconfirmed',
              status: TransferStatus.resultUnconfirmed,
            ),
            _transfer(id: 'cancelled', status: TransferStatus.cancelled),
          ],
          onPause: (_) {},
          onRetry: (_) async {},
          onDelete: (id) async => deleted.add(id),
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('transfer-more-paused')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('transfer-delete-paused')));
    await tester.pumpAndSettle();
    expect(find.text('取消并删除传输？'), findsOneWidget);
    expect(find.textContaining('删除保存在本机的可恢复进度'), findsOneWidget);
    expect(find.textContaining('服务端存在上传会话'), findsOneWidget);
    await tester.tap(find.text('保留传输'));
    await tester.pumpAndSettle();
    expect(deleted, isEmpty);

    await tester.tap(find.byKey(const Key('transfer-more-paused')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('transfer-delete-paused')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('transfer-confirm-delete')));
    await tester.pumpAndSettle();
    expect(deleted, <String>['paused']);

    await tester.tap(
      find.byKey(const Key('transfer-review-remove-unconfirmed')),
    );
    await tester.pumpAndSettle();
    expect(find.text('移除待确认记录？'), findsOneWidget);
    expect(find.textContaining('请先确认目标位置'), findsOneWidget);
    await tester.tap(find.byKey(const Key('transfer-confirm-review-remove')));
    await tester.pumpAndSettle();
    expect(deleted, <String>['paused', 'unconfirmed']);

    await tester.tap(find.byKey(const Key('transfer-clear-cancelled')));
    await tester.pump();
    expect(deleted, <String>['paused', 'unconfirmed', 'cancelled']);
  });

  testWidgets('provides named progress semantics and supports large text', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(390, 844));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final semantics = tester.ensureSemantics();

    await tester.pumpWidget(
      _app(
        TransferCenter(
          transfers: <ClientTransfer>[
            _transfer(
              id: 'running',
              name: '家庭照片.zip',
              direction: TransferDirection.upload,
              status: TransferStatus.running,
              transferred: 512,
              total: 1024,
            ),
          ],
          onPause: (_) {},
          onRetry: (_) async {},
          onDelete: (_) async {},
        ),
        textScaler: const TextScaler.linear(2),
      ),
    );

    expect(
      find.bySemanticsLabel('上传 家庭照片.zip，50% · 512 B / 1.0 KB'),
      findsOneWidget,
    );
    expect(find.bySemanticsLabel('上传 家庭照片.zip进度'), findsOneWidget);
    expect(find.byTooltip('暂停 家庭照片.zip'), findsOneWidget);
    expect(tester.takeException(), isNull);
    expect(
      tester.getSize(find.byKey(const Key('transfer-center-surface'))).height,
      lessThanOrEqualTo(844),
    );
    semantics.dispose();
  });

  testWidgets('caps the transfer center width on desktop', (tester) async {
    await tester.binding.setSurfaceSize(const Size(1280, 900));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    await tester.pumpWidget(
      _app(
        TransferCenter(
          transfers: const <ClientTransfer>[],
          onPause: (_) {},
          onRetry: (_) async {},
          onDelete: (_) async {},
        ),
      ),
    );

    expect(
      tester.getSize(find.byKey(const Key('transfer-center-surface'))).width,
      lessThanOrEqualTo(TransferCenter.maxWidth),
    );
    expect(find.text('暂无传输记录'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('disables a task while an asynchronous action is pending', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(390, 844));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final pending = Completer<void>();
    var retryCount = 0;

    await tester.pumpWidget(
      _app(
        TransferCenter(
          transfers: <ClientTransfer>[
            _transfer(id: 'failed', status: TransferStatus.failed),
          ],
          onPause: (_) {},
          onRetry: (_) {
            retryCount++;
            return pending.future;
          },
          onDelete: (_) async {},
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('transfer-retry-failed')));
    await tester.pump();

    expect(retryCount, 1);
    expect(find.byKey(const Key('transfer-retry-failed')), findsNothing);
    expect(find.bySemanticsLabel('正在处理 failed.txt'), findsOneWidget);

    pending.complete();
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('transfer-retry-failed')), findsOneWidget);
  });

  testWidgets('does not update a disposed center after pending work finishes', (
    tester,
  ) async {
    final pending = Completer<void>();

    await tester.pumpWidget(
      _app(
        TransferCenter(
          transfers: <ClientTransfer>[
            _transfer(id: 'failed', status: TransferStatus.failed),
          ],
          onPause: (_) {},
          onRetry: (_) => pending.future,
          onDelete: (_) async {},
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('transfer-retry-failed')));
    await tester.pump();
    await tester.pumpWidget(const SizedBox.shrink());

    pending.complete();
    await tester.pump();

    expect(tester.takeException(), isNull);
  });

  testWidgets('keeps crowded actions usable on a narrow large-text screen', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(320, 720));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    await tester.pumpWidget(
      _app(
        TransferCenter(
          transfers: <ClientTransfer>[
            _transfer(
              id: 'paused',
              name: '家庭照片归档-2026-超长文件名.zip',
              status: TransferStatus.paused,
            ),
            _transfer(
              id: 'failed',
              name: '需要重新传输的长文件名称.bin',
              status: TransferStatus.failed,
            ),
            _transfer(
              id: 'destination',
              name: '等待选择保存位置的长文件名称.pdf',
              status: TransferStatus.awaitingDestination,
            ),
          ],
          onPause: (_) {},
          onRetry: (_) async {},
          onDelete: (_) async {},
        ),
        textScaler: const TextScaler.linear(2),
      ),
    );

    expect(tester.takeException(), isNull);
    expect(find.byKey(const Key('transfer-more-paused')), findsOneWidget);
    await tester.tap(find.byKey(const Key('transfer-more-paused')));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    await tester.tap(find.byKey(const Key('transfer-delete-paused')));
    await tester.pumpAndSettle();
    expect(find.text('取消并删除传输？'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

Widget _app(Widget child, {TextScaler textScaler = TextScaler.noScaling}) {
  return MaterialApp(
    theme: MnemoTheme.light,
    builder: (context, child) => MediaQuery(
      data: MediaQuery.of(context).copyWith(textScaler: textScaler),
      child: child!,
    ),
    home: Scaffold(body: child),
  );
}

ClientTransfer _transfer({
  required String id,
  String? name,
  TransferDirection direction = TransferDirection.download,
  required TransferStatus status,
  int transferred = 512,
  int total = 1024,
  bool? canRetry,
}) {
  return ClientTransfer(
    id: id,
    name: name ?? '$id.txt',
    direction: direction,
    status: status,
    transferred: transferred,
    total: total,
    canRetry: canRetry ?? status == TransferStatus.failed,
  );
}
