import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/app/client_app.dart';
import 'package:mnemonas_client/app/client_controller.dart';
import 'package:mnemonas_client/app/client_state.dart';

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
}

final class _TestClientController extends ClientController {
  @override
  ClientState build() {
    return const ClientState(stage: ClientStage.needsConnection);
  }
}
