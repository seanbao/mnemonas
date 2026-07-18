import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/design_system/design_system.dart';
import 'package:mnemonas_client/features/account/account_page.dart';

void main() {
  testWidgets(
    'shows development status and issue feedback without contribution',
    (WidgetTester tester) async {
      var reportCalls = 0;
      await tester.pumpWidget(
        MaterialApp(
          theme: MnemoTheme.light,
          home: Scaffold(
            body: AccountPage(
              viewModel: const AccountViewModel(
                username: 'admin',
                roleLabel: '管理员',
                deviceName: '家庭存储',
                serverAddress: 'https://nas.example.com',
                clientVersion: '0.1.0+1',
              ),
              onChangeServer: () {},
              onLogout: () async {},
              onReportIssue: () => reportCalls++,
            ),
          ),
        ),
      );

      expect(find.text('当前仍在开发阶段，尚未发布可用版本。'), findsOneWidget);
      expect(find.text('提交问题反馈'), findsOneWidget);
      expect(find.textContaining('贡献'), findsNothing);

      final Finder reportIssue = find.byKey(const Key('account-report-issue'));
      await tester.drag(
        find.byKey(const Key('account-page')),
        const Offset(0, -420),
      );
      await tester.pumpAndSettle();
      await tester.tap(reportIssue);
      expect(reportCalls, 1);
    },
  );
}
