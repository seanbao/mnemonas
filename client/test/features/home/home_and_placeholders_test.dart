import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/home/home_page.dart';
import 'package:mnemonas_client/features/photos/photos_page.dart';

void main() {
  testWidgets('home presents device, storage, alerts, and recent activity', (
    WidgetTester tester,
  ) async {
    var openedFiles = false;
    await tester.pumpWidget(
      _app(
        HomePage(
          viewModel: HomeViewModel.ready(
            HomeSummary(
              deviceName: '家庭存储',
              serverAddress: 'https://nas.example.com',
              connectionStatus: NasConnectionStatus.online,
              storage: const StorageSummary(
                usedBytes: 512 * 1024 * 1024,
                totalBytes: 1024 * 1024 * 1024,
              ),
              alerts: const <HomeAlert>[
                HomeAlert(id: 'disk', title: '存储空间提醒', message: '剩余空间低于设定阈值'),
              ],
              activities: <HomeActivity>[
                HomeActivity(
                  title: '上传完成',
                  detail: '说明.pdf',
                  occurredAt: DateTime.now(),
                  type: HomeActivityType.upload,
                ),
              ],
            ),
          ),
          onRefresh: () async {},
          onOpenFiles: () => openedFiles = true,
        ),
      ),
    );

    expect(find.text('家庭存储'), findsOneWidget);
    expect(find.text('已连接'), findsOneWidget);
    expect(find.textContaining('512 MB'), findsOneWidget);
    expect(find.text('存储空间提醒'), findsOneWidget);
    expect(find.text('上传完成'), findsOneWidget);

    await tester.tap(find.text('存储空间'));
    expect(openedFiles, isTrue);
  });

  testWidgets('photos explicitly reports the unimplemented index', (
    WidgetTester tester,
  ) async {
    var openedFiles = false;
    await tester.pumpWidget(
      _app(
        PhotosPage(
          viewModel: const PhotosViewModel.unavailable(),
          onBrowseImageFiles: () => openedFiles = true,
        ),
      ),
    );

    expect(find.text('相册功能尚未接入'), findsOneWidget);
    expect(find.textContaining('图片仍可从文件页'), findsOneWidget);
    await tester.tap(find.text('前往文件'));
    expect(openedFiles, isTrue);
  });
}

Widget _app(Widget child) {
  return MaterialApp(
    theme: MnemoTheme.light,
    home: Scaffold(body: child),
  );
}
