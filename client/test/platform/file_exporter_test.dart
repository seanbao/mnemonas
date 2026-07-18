import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/platform/file_exporter.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  const channel = MethodChannel('com.mnemonas.app/file_export');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  setUp(() {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
  });

  tearDown(() {
    messenger.setMockMethodCallHandler(channel, null);
    debugDefaultTargetPlatformOverride = null;
  });

  test('chooseTarget returns a validated content target', () async {
    MethodCall? receivedCall;
    messenger.setMockMethodCallHandler(channel, (call) async {
      receivedCall = call;
      return 'content://documents/document/export-one';
    });

    final target = await FileExporter.chooseTarget(
      suggestedName: 'report.bin',
      mimeType: 'application/octet-stream',
    );

    expect(receivedCall?.method, 'createDocument');
    expect(receivedCall?.arguments, <String, Object?>{
      'suggestedName': 'report.bin',
      'mimeType': 'application/octet-stream',
    });
    expect(target?.contentUri, 'content://documents/document/export-one');
    expect(target?.requiresPrivateStaging, isTrue);
  });

  test('exportStagedFile forwards progress for its operation', () async {
    final started = Completer<void>();
    final release = Completer<void>();
    MethodCall? receivedCall;
    messenger.setMockMethodCallHandler(channel, (call) async {
      receivedCall = call;
      if (call.method == 'copyToDocument') {
        started.complete();
        await release.future;
      }
      return null;
    });
    final progress = <(int, int?)>[];

    final export = FileExporter.exportStagedFile(
      sourcePath: '/tmp/mnemonas-export',
      target: FileExportTarget.contentUri(
        'content://documents/document/export-two',
      ),
      operationId: 'export-progress',
      onProgress: (transferred, total) {
        progress.add((transferred, total));
      },
    );
    await started.future;
    await messenger.handlePlatformMessage(
      channel.name,
      const StandardMethodCodec().encodeMethodCall(
        const MethodCall('copyProgress', <String, Object?>{
          'operationId': 'export-progress',
          'transferred': 2048,
          'total': 8192,
        }),
      ),
      null,
    );
    release.complete();
    await export;

    expect(receivedCall?.method, 'copyToDocument');
    expect(receivedCall?.arguments, <String, Object?>{
      'operationId': 'export-progress',
      'sourcePath': '/tmp/mnemonas-export',
      'targetUri': 'content://documents/document/export-two',
    });
    expect(progress, <(int, int?)>[(2048, 8192)]);
  });

  test('cancelExport forwards a validated operation ID', () async {
    final calls = <MethodCall>[];
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      return null;
    });

    await FileExporter.cancelExport('export-cancel');

    expect(calls, hasLength(1));
    expect(calls.first.method, 'cancelExport');
    expect(calls.first.arguments, <String, Object?>{
      'operationId': 'export-cancel',
    });
  });

  test('content targets reject malformed URIs', () {
    expect(
      () => FileExportTarget.contentUri('file:///tmp/export'),
      throwsFormatException,
    );
    expect(
      () => FileExportTarget.contentUri('content:///missing-authority'),
      throwsFormatException,
    );
  });

  test('native export controls reject non-Android platforms', () {
    debugDefaultTargetPlatformOverride = TargetPlatform.linux;
    expect(
      () => FileExporter.exportStagedFile(
        sourcePath: '/tmp/mnemonas-export',
        target: FileExportTarget.contentUri(
          'content://documents/document/export-four',
        ),
        operationId: 'export-linux',
      ),
      throwsStateError,
    );
    expect(
      () => FileExporter.cancelExport('export-linux'),
      throwsUnsupportedError,
    );
  });
}
