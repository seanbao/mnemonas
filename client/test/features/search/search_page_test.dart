import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/search/search_models.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/search/search_page.dart';

void main() {
  testWidgets('renders idle, loading, error, and empty states', (
    WidgetTester tester,
  ) async {
    final List<String> searches = <String>[];

    await tester.pumpWidget(
      _app(_page(viewModel: const SearchViewModel.idle())),
    );
    expect(find.byKey(const Key('search-idle')), findsOneWidget);
    expect(find.text('搜索文件'), findsOneWidget);

    await tester.pumpWidget(
      _app(_page(viewModel: const SearchViewModel.loading('报告'))),
    );
    expect(find.byKey(const Key('search-loading')), findsOneWidget);
    expect(find.byKey(const Key('search-progress')), findsOneWidget);

    await tester.pumpWidget(
      _app(
        _page(
          viewModel: const SearchViewModel.error(
            query: '报告',
            message: '设备暂时不可用',
          ),
          onSearch: (String query) async => searches.add(query),
        ),
      ),
    );
    expect(find.byKey(const Key('search-error')), findsOneWidget);
    expect(find.text('设备暂时不可用'), findsOneWidget);
    await tester.tap(find.text('重试'));
    await tester.pump();
    expect(searches, <String>['报告']);

    await tester.pumpWidget(
      _app(
        _page(
          viewModel: SearchViewModel.ready(
            listing: _listing(query: '报告', count: 0),
          ),
        ),
      ),
    );
    expect(find.byKey(const Key('search-empty')), findsOneWidget);
    expect(find.text('未找到匹配项'), findsOneWidget);
    expect(find.textContaining('名称包含“报告”'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('debounces typing for 350ms and submits IME search immediately', (
    WidgetTester tester,
  ) async {
    final List<String> searches = <String>[];
    var clearCount = 0;
    await tester.pumpWidget(
      _app(
        _page(
          viewModel: const SearchViewModel.idle(),
          onSearch: (String query) async => searches.add(query),
          onClear: () => clearCount++,
        ),
      ),
    );

    final Finder input = find.byKey(const Key('search-query-input'));
    await tester.enterText(input, ' 报告 ');
    await tester.pump(const Duration(milliseconds: 349));
    expect(searches, isEmpty);
    await tester.pump(const Duration(milliseconds: 1));
    expect(searches, <String>['报告']);

    await tester.enterText(input, '照片');
    await tester.testTextInput.receiveAction(TextInputAction.search);
    await tester.pump();
    expect(searches, <String>['报告', '照片']);
    await tester.pump(const Duration(milliseconds: 350));
    expect(searches, <String>['报告', '照片']);

    final int clearsBeforeButton = clearCount;
    await tester.tap(find.byKey(const Key('search-clear')));
    await tester.pump();
    expect(clearCount, clearsBeforeButton + 1);
    expect(tester.widget<TextField>(input).controller!.text, isEmpty);
  });

  testWidgets('clears mismatched results before starting the debounce window', (
    WidgetTester tester,
  ) async {
    final List<String> searches = <String>[];
    var clearCount = 0;
    await tester.pumpWidget(
      _app(
        _page(
          viewModel: SearchViewModel.ready(
            listing: _listing(query: '旧文件', count: 1),
          ),
          onSearch: (String query) async => searches.add(query),
          onClear: () => clearCount++,
        ),
      ),
    );

    await tester.enterText(find.byKey(const Key('search-query-input')), '新文件');
    await tester.pump();

    expect(clearCount, 1);
    expect(searches, isEmpty);
    await tester.pump(const Duration(milliseconds: 350));
    expect(searches, <String>['新文件']);
  });

  testWidgets('rejects a query longer than 100 Unicode characters', (
    WidgetTester tester,
  ) async {
    final List<String> searches = <String>[];
    var clearCount = 0;
    await tester.pumpWidget(
      _app(
        _page(
          viewModel: const SearchViewModel.idle(),
          onSearch: (String query) async => searches.add(query),
          onClear: () => clearCount++,
        ),
      ),
    );

    await tester.enterText(
      find.byKey(const Key('search-query-input')),
      List<String>.filled(101, '文').join(),
    );
    await tester.pump(const Duration(milliseconds: 400));

    expect(find.byKey(const Key('search-validation')), findsOneWidget);
    expect(find.text('搜索内容不能超过 100 个字符'), findsWidgets);
    expect(searches, isEmpty);
    expect(clearCount, 1);
  });

  testWidgets('keeps stale results visible and explains the 100 item cap', (
    WidgetTester tester,
  ) async {
    var refreshCount = 0;
    await tester.pumpWidget(
      _app(
        _page(
          viewModel: SearchViewModel.ready(
            listing: _listing(query: '报告', count: 100),
            refreshErrorMessage: '设备暂时不可用。',
          ),
          onSearch: (String query) async => refreshCount++,
        ),
      ),
    );

    expect(find.byKey(const Key('search-results')), findsOneWidget);
    expect(find.byKey(const Key('search-refresh-error')), findsOneWidget);
    expect(find.textContaining('当前显示上一次成功加载的结果'), findsOneWidget);
    expect(find.byKey(const Key('search-limit-notice')), findsOneWidget);
    expect(find.textContaining('结果可能未完全列出'), findsOneWidget);
    expect(find.text('100 项'), findsOneWidget);
    expect(find.byKey(const Key('search-result-/文档/报告_0.pdf')), findsOneWidget);

    await tester.tap(find.text('重试'));
    await tester.pump();
    expect(refreshCount, 1);
    expect(tester.takeException(), isNull);
  });

  testWidgets('prevents duplicate result activation while opening', (
    WidgetTester tester,
  ) async {
    final Completer<void> opening = Completer<void>();
    final List<String> openedPaths = <String>[];
    await tester.pumpWidget(
      _app(
        _page(
          viewModel: SearchViewModel.ready(
            listing: _listing(query: '报告', count: 2),
          ),
          onOpenResult: (SearchResultItem result) {
            openedPaths.add(result.path);
            return opening.future;
          },
        ),
      ),
    );

    final Finder first = find.byKey(const Key('search-result-/文档/报告_0.pdf'));
    await tester.tap(first);
    await tester.pump();
    expect(find.byKey(const Key('search-result-opening')), findsOneWidget);

    await tester.tap(first);
    await tester.pump();
    expect(openedPaths, <String>['/文档/报告_0.pdf']);

    opening.complete();
    await tester.pump();
    expect(find.byKey(const Key('search-result-opening')), findsNothing);
  });

  testWidgets('close button and system back invoke the close callback', (
    WidgetTester tester,
  ) async {
    var closeCount = 0;
    await tester.pumpWidget(
      _app(
        _page(
          viewModel: const SearchViewModel.idle(),
          onClose: () => closeCount++,
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('search-close')));
    await tester.pump();
    expect(closeCount, 1);

    await tester.binding.handlePopRoute();
    await tester.pump();
    expect(closeCount, 2);
  });

  testWidgets('does not overflow on compact and expanded windows', (
    WidgetTester tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(320, 640));
    addTearDown(() => tester.binding.setSurfaceSize(null));

    await tester.pumpWidget(
      _app(
        _page(
          viewModel: SearchViewModel.ready(
            listing: _listing(query: '家庭', count: 3, longNames: true),
          ),
        ),
      ),
    );
    expect(find.byKey(const Key('search-results')), findsOneWidget);
    expect(tester.takeException(), isNull);

    await tester.binding.setSurfaceSize(const Size(1280, 900));
    await tester.pump();
    expect(find.byKey(const Key('search-results')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

SearchPage _page({
  required SearchViewModel viewModel,
  Future<void> Function(String query)? onSearch,
  VoidCallback? onClear,
  VoidCallback? onClose,
  Future<void> Function(SearchResultItem result)? onOpenResult,
}) {
  return SearchPage(
    viewModel: viewModel,
    onSearch: onSearch ?? (_) async {},
    onClear: onClear ?? () {},
    onClose: onClose ?? () {},
    onOpenResult: onOpenResult ?? (_) async {},
  );
}

SearchListing _listing({
  required String query,
  required int count,
  bool longNames = false,
}) {
  final List<Map<String, Object?>> results =
      List<Map<String, Object?>>.generate(count, (int index) {
        final String name = longNames
            ? '${query}_这是一段用于验证小屏布局不会溢出的很长文件名_$index.pdf'
            : '${query}_$index.pdf';
        return <String, Object?>{
          'name': name,
          'path': '/文档/$name',
          'isDir': false,
          'size': 2048 + index,
          'modTime': '2026-07-19T10:30:00+08:00',
          'hash': null,
        };
      }, growable: false);
  return SearchListing.fromJson(
    <String, Object?>{'query': query, 'results': results, 'count': count},
    expectedQuery: query,
    limit: 100,
  );
}

Widget _app(Widget child) {
  return MaterialApp(
    theme: MnemoTheme.light,
    home: Scaffold(body: child),
  );
}
