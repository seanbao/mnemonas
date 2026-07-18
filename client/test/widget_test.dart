import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/app/client_app.dart';
import 'package:mnemonas_client/app/client_controller.dart';
import 'package:mnemonas_client/app/client_state.dart';
import 'package:mnemonas_client/core/files/file_models.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/search/search_models.dart';
import 'package:mnemonas_client/features/files/files_page.dart';

void main() {
  testWidgets('starts at the server connection page', (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          clientControllerProvider.overrideWith(_TestClientController.new),
        ],
        child: const MnemoNasClientApp(),
      ),
    );

    expect(find.text('连接到设备'), findsOneWidget);
    expect(find.text('验证并继续'), findsOneWidget);
  });

  testWidgets('closing search fences a delayed result activation', (
    WidgetTester tester,
  ) async {
    final started = Completer<void>();
    final release = Completer<void>();
    late _DelayedSearchController controller;
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          clientControllerProvider.overrideWith(() {
            return controller = _DelayedSearchController(
              started: started,
              release: release,
            );
          }),
        ],
        child: const MnemoNasClientApp(),
      ),
    );

    await tester.tap(find.text('文件').last);
    await tester.pump();
    expect(find.byType(FilesPage), findsOneWidget);
    await tester.tap(find.byKey(const Key('app-shell-search')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('search-result-/资料')));
    await started.future.timeout(const Duration(seconds: 2));

    await tester.tap(find.byKey(const Key('search-close')));
    await tester.pump();
    expect(find.byType(FilesPage), findsOneWidget);

    release.complete();
    await tester.pumpAndSettle();

    expect(controller.loadedPaths, <String>['/资料']);
    expect(controller.presentedDirectories, isEmpty);
    expect(controller.state.currentPath, '/原目录');
    expect(find.byType(FilesPage), findsOneWidget);
    expect(find.byKey(const Key('app-shell-search')), findsOneWidget);
  });

  testWidgets('a mismatched directory reload does not open a search result', (
    WidgetTester tester,
  ) async {
    final started = Completer<void>();
    final release = Completer<void>();
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          clientControllerProvider.overrideWith(
            () => _DelayedSearchController(
              started: started,
              release: release,
              loadedPath: '/其他',
            ),
          ),
        ],
        child: const MnemoNasClientApp(),
      ),
    );

    await tester.tap(find.byKey(const Key('app-shell-search')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('search-result-/资料')));
    await started.future.timeout(const Duration(seconds: 2));
    release.complete();
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('search-query-input')), findsOneWidget);
    expect(find.byType(FilesPage), findsNothing);
    expect(find.text('文件夹可能已移动或删除，请重新搜索。'), findsOneWidget);
  });
}

final class _TestClientController extends ClientController {
  @override
  ClientState build() {
    return const ClientState(stage: ClientStage.needsConnection);
  }
}

final class _DelayedSearchController extends ClientController {
  _DelayedSearchController({
    required this.started,
    required this.release,
    this.loadedPath = '/资料',
  });

  final Completer<void> started;
  final Completer<void> release;
  final String loadedPath;
  final List<String> loadedPaths = <String>[];
  final List<String> presentedDirectories = <String>[];

  @override
  ClientState build() {
    return ClientState(
      stage: ClientStage.ready,
      currentPath: '/原目录',
      directory: _directoryListing('/原目录'),
      searchQuery: '资料',
      search: SearchListing.fromJson(
        <String, Object?>{
          'query': '资料',
          'results': <Map<String, Object?>>[
            <String, Object?>{
              'name': '资料',
              'path': '/资料',
              'isDir': true,
              'size': 0,
              'modTime': '2026-07-19T10:30:00+08:00',
            },
          ],
          'count': 1,
        },
        expectedQuery: '资料',
        limit: 100,
      ),
    );
  }

  @override
  Future<ApiResponse<DirectoryListing>> resolveDirectoryForSearch(
    String path,
  ) async {
    loadedPaths.add(path);
    if (!started.isCompleted) {
      started.complete();
    }
    await release.future;
    return ApiResponse<DirectoryListing>(
      data: _directoryListing(loadedPath),
      statusCode: 200,
    );
  }

  @override
  void presentDirectoryListing(
    DirectoryListing listing, {
    bool persistenceWarning = false,
  }) {
    presentedDirectories.add(listing.path);
    state = state.copyWith(
      currentPath: listing.path,
      directory: listing,
      isBusy: false,
    );
  }

  @override
  void cancelSearch() {}
}

DirectoryListing _directoryListing(String path) {
  return DirectoryListing.fromJson(<String, Object?>{
    'path': path,
    'files': <Object?>[],
    'capabilities': <String, Object?>{
      'read': true,
      'concreteRead': true,
      'write': true,
    },
    'deleteMode': 'trash',
    'deletePolicyToken':
        'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    'trashRetentionDays': 30,
    'trashAutoCleanupEnabled': true,
  });
}
