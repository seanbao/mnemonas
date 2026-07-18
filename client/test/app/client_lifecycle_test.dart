import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/app/client_app.dart';
import 'package:mnemonas_client/app/client_controller.dart';
import 'package:mnemonas_client/app/client_state.dart';

void main() {
  testWidgets(
    'pauses foreground transfers once for each background lifecycle cycle',
    (tester) async {
      late _LifecycleController controller;
      addTearDown(() {
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.resumed,
        );
      });

      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            clientControllerProvider.overrideWith(() {
              return controller = _LifecycleController();
            }),
          ],
          child: const MnemoNasClientApp(),
        ),
      );

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      await tester.pump();
      expect(controller.backgroundPauseCalls, 0);

      for (final state in <AppLifecycleState>[
        AppLifecycleState.hidden,
        AppLifecycleState.paused,
        AppLifecycleState.detached,
      ]) {
        tester.binding.handleAppLifecycleStateChanged(state);
        await tester.pump();
      }
      expect(controller.backgroundPauseCalls, 1);

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pump();
      expect(controller.backgroundPauseCalls, 1);
      expect(controller.resumeTransferCalls, 0);

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      await tester.pump();
      expect(controller.backgroundPauseCalls, 1);

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      await tester.pump();
      expect(controller.backgroundPauseCalls, 2);
      expect(controller.resumeTransferCalls, 0);
    },
  );

  testWidgets(
    'retries a failed upload-preparation cancellation within one background cycle',
    (tester) async {
      var cancellationCalls = 0;
      final materialization = Completer<File>();
      addTearDown(() {
        tester.binding.handleAppLifecycleStateChanged(
          AppLifecycleState.resumed,
        );
      });

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: buildUploadPreparationDialogForTesting(
              name: '家庭照片.zip',
              materialize: (_) => materialization.future,
              cancel: () async {
                cancellationCalls++;
                if (cancellationCalls == 1) {
                  throw StateError('native copy cancellation was busy');
                }
              },
            ),
          ),
        ),
      );

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      await tester.pump();
      await tester.pump();
      expect(cancellationCalls, 1);

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pump();
      await tester.pump();
      expect(cancellationCalls, 2);

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.detached);
      await tester.pump();
      expect(cancellationCalls, 2);
    },
  );
}

final class _LifecycleController extends ClientController {
  var backgroundPauseCalls = 0;
  var resumeTransferCalls = 0;

  @override
  ClientState build() {
    return const ClientState(stage: ClientStage.ready);
  }

  @override
  Future<int> pauseActiveTransfersForAppBackground() async {
    backgroundPauseCalls++;
    return 0;
  }

  @override
  Future<void> resumeTransfer(String id) async {
    resumeTransferCalls++;
  }
}
