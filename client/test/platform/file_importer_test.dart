import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/platform/file_importer.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  const channel = MethodChannel('com.mnemonas.app/file_import');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  setUp(() {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
  });

  tearDown(() {
    messenger.setMockMethodCallHandler(channel, null);
    debugDefaultTargetPlatformOverride = null;
  });

  test('pickDocuments decodes document metadata', () async {
    messenger.setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'pickDocuments');
      expect(call.arguments, isNull);
      return <Object?>[
        <String, Object?>{
          'uri': 'content://documents/document/one',
          'displayName': 'one.txt',
          'mimeType': 'text/plain',
          'size': 12,
        },
        <String, Object?>{
          'uri': 'content://documents/document/two',
          'displayName': 'two.bin',
          'mimeType': null,
          'size': null,
        },
      ];
    });

    final sources = await FileImporter.pickDocuments();

    expect(sources, hasLength(2));
    expect(sources.first.uri, 'content://documents/document/one');
    expect(sources.first.displayName, 'one.txt');
    expect(sources.first.mimeType, 'text/plain');
    expect(sources.first.size, 12);
    expect(sources.last.mimeType, isNull);
    expect(sources.last.size, isNull);
    expect(() => sources.add(sources.first), throwsUnsupportedError);
  });

  test('pickDocuments returns an empty list after cancellation', () async {
    messenger.setMockMethodCallHandler(channel, (_) async => <Object?>[]);

    expect(await FileImporter.pickDocuments(), isEmpty);
  });

  test('pickDocuments rejects duplicate URIs', () async {
    messenger.setMockMethodCallHandler(channel, (_) async {
      return <Object?>[
        <String, Object?>{
          'uri': 'content://documents/document/one',
          'displayName': 'one.txt',
          'mimeType': 'text/plain',
          'size': 12,
        },
        <String, Object?>{
          'uri': 'content://documents/document/one',
          'displayName': 'duplicate.txt',
          'mimeType': 'text/plain',
          'size': 12,
        },
      ];
    });

    await expectLater(
      FileImporter.pickDocuments(),
      throwsA(isA<FormatException>()),
    );
  });

  test('pickDocuments rejects incomplete metadata', () async {
    messenger.setMockMethodCallHandler(channel, (_) async {
      return <Object?>[
        <String, Object?>{
          'uri': 'content://documents/document/one',
          'mimeType': 'text/plain',
          'size': 12,
        },
      ];
    });

    await expectLater(
      FileImporter.pickDocuments(),
      throwsA(isA<FormatException>()),
    );
  });

  test('copyDocumentToFile forwards the validated request', () async {
    MethodCall? receivedCall;
    messenger.setMockMethodCallHandler(channel, (call) async {
      receivedCall = call;
      return null;
    });

    final file = await FileImporter.copyDocumentToFile(
      uri: 'content://documents/document/one',
      destinationPath: '/tmp/mnemonas-import',
      operationId: 'import-1',
      expectedLength: 12,
      maxBytes: 1024,
    );

    expect(file.path, '/tmp/mnemonas-import');
    expect(receivedCall?.method, 'copyDocumentToFile');
    expect(receivedCall?.arguments, <String, Object?>{
      'operationId': 'import-1',
      'uri': 'content://documents/document/one',
      'destinationPath': '/tmp/mnemonas-import',
      'expectedLength': 12,
      'maxBytes': 1024,
    });
  });

  test('copyDocumentToFile rejects invalid and oversized lengths', () async {
    var invoked = false;
    messenger.setMockMethodCallHandler(channel, (_) async {
      invoked = true;
      return null;
    });

    await expectLater(
      FileImporter.copyDocumentToFile(
        uri: 'content://documents/document/one',
        destinationPath: '/tmp/mnemonas-import',
        operationId: 'import-negative',
        expectedLength: -1,
        maxBytes: 1024,
      ),
      throwsArgumentError,
    );
    await expectLater(
      FileImporter.copyDocumentToFile(
        uri: 'content://documents/document/one',
        destinationPath: '/tmp/mnemonas-import',
        operationId: 'import-limit-invalid',
        maxBytes: 0,
      ),
      throwsArgumentError,
    );
    await expectLater(
      FileImporter.copyDocumentToFile(
        uri: 'content://documents/document/one',
        destinationPath: '/tmp/mnemonas-import',
        operationId: 'import-too-large',
        expectedLength: 1025,
        maxBytes: 1024,
      ),
      throwsArgumentError,
    );
    expect(invoked, isFalse);
  });

  test('copyDocumentToFile forwards a hard limit for unknown sizes', () async {
    MethodCall? receivedCall;
    messenger.setMockMethodCallHandler(channel, (call) async {
      receivedCall = call;
      return null;
    });

    await FileImporter.copyDocumentToFile(
      uri: 'content://documents/document/unknown',
      destinationPath: '/tmp/mnemonas-import-unknown',
      operationId: 'import-unknown',
      maxBytes: 10 * 1024 * 1024 * 1024,
    );

    expect(receivedCall?.arguments, <String, Object?>{
      'operationId': 'import-unknown',
      'uri': 'content://documents/document/unknown',
      'destinationPath': '/tmp/mnemonas-import-unknown',
      'expectedLength': null,
      'maxBytes': 10 * 1024 * 1024 * 1024,
    });
  });

  test('copyDocumentToFile delivers operation progress', () async {
    final started = Completer<void>();
    final release = Completer<void>();
    messenger.setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'copyDocumentToFile') {
        started.complete();
        await release.future;
      }
      return null;
    });
    final progress = <(int, int?)>[];

    final copy = FileImporter.copyDocumentToFile(
      uri: 'content://documents/document/one',
      destinationPath: '/tmp/mnemonas-import-progress',
      operationId: 'import-progress',
      expectedLength: 4096,
      maxBytes: 8192,
      onProgress: (transferred, total) {
        progress.add((transferred, total));
      },
    );
    await started.future;
    await messenger.handlePlatformMessage(
      channel.name,
      const StandardMethodCodec().encodeMethodCall(
        const MethodCall('copyProgress', <String, Object?>{
          'operationId': 'import-progress',
          'transferred': 1024,
          'total': 4096,
        }),
      ),
      null,
    );
    release.complete();
    await copy;

    expect(progress, <(int, int?)>[(1024, 4096)]);
  });

  test('cancelCopy forwards a validated operation ID', () async {
    MethodCall? receivedCall;
    messenger.setMockMethodCallHandler(channel, (call) async {
      receivedCall = call;
      return null;
    });

    await FileImporter.cancelCopy('import-cancel');

    expect(receivedCall?.method, 'cancelCopy');
    expect(receivedCall?.arguments, <String, Object?>{
      'operationId': 'import-cancel',
    });
  });

  test('non-Android platforms are rejected before invoking the channel', () {
    debugDefaultTargetPlatformOverride = TargetPlatform.linux;
    var invoked = false;
    messenger.setMockMethodCallHandler(channel, (_) async {
      invoked = true;
      return null;
    });

    expect(FileImporter.pickDocuments, throwsUnsupportedError);
    expect(
      () => FileImporter.copyDocumentToFile(
        uri: 'content://documents/document/one',
        destinationPath: '/tmp/mnemonas-import',
        operationId: 'import-linux',
        maxBytes: 1024,
      ),
      throwsUnsupportedError,
    );
    expect(
      () => FileImporter.cancelCopy('import-linux'),
      throwsUnsupportedError,
    );
    expect(invoked, isFalse);
  });
}
