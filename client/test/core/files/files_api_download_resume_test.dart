import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/files/files_api.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/network/api_error.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

void main() {
  test('new download records its validator and durable offset', () async {
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-download-new-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final adapter = _DownloadAdapter((options) {
      expect(options.headers['Range'], isNull);
      expect(options.headers[downloadIdentityConditionHeader], isNull);
      return _downloadResponse(<int>[1, 2, 3, 4], validator: 'identity-1');
    });
    final api = await _filesApi(adapter);
    final checkpoints = <DownloadCheckpoint>[];

    final result = await api.downloadFile(
      logicalPath: '/payload.bin',
      destinationPath: '${directory.path}/payload.bin',
      stagingPath: '${directory.path}/task.part',
      expectedTotalBytes: 4,
      preservePartialOnFailure: true,
      onCheckpoint: checkpoints.add,
    );

    expect(await result.file.readAsBytes(), <int>[1, 2, 3, 4]);
    expect(result.validator, 'identity-1');
    expect(result.totalBytes, 4);
    expect(result.bytesWritten, 4);
    expect(checkpoints, hasLength(2));
    expect(checkpoints.first.validator, 'identity-1');
    expect(checkpoints.first.durableOffset, 0);
    expect(checkpoints.last.durableOffset, 4);
    expect(checkpoints.last.totalBytes, 4);
    expect(File('${directory.path}/task.part').existsSync(), isFalse);
  });

  test('download resumes only an exact validated range', () async {
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-download-resume-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final part = File('${directory.path}/task.part');
    await part.writeAsBytes(<int>[1, 2, 3], flush: true);
    final adapter = _DownloadAdapter((options) {
      expect(options.headers['Range'], 'bytes=3-');
      expect(
        options.headers[downloadIdentityConditionHeader],
        'identity-stable',
      );
      return _downloadResponse(
        <int>[4, 5, 6],
        statusCode: HttpStatus.partialContent,
        validator: 'identity-stable',
        contentRange: 'bytes 3-5/6',
      );
    });
    final api = await _filesApi(adapter);

    final result = await api.downloadFile(
      logicalPath: '/payload.bin',
      destinationPath: '${directory.path}/payload.bin',
      stagingPath: part.path,
      resumeValidator: 'identity-stable',
      expectedTotalBytes: 6,
      preservePartialOnFailure: true,
    );

    expect(await result.file.readAsBytes(), <int>[1, 2, 3, 4, 5, 6]);
    expect(result.bytesWritten, 6);
    expect(part.existsSync(), isFalse);
  });

  test(
    'a complete durable partial is committed without another request',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'mnemonas-download-complete-partial-',
      );
      addTearDown(() => directory.delete(recursive: true));
      final part = File('${directory.path}/task.part');
      await part.writeAsBytes(<int>[1, 2, 3, 4], flush: true);
      var requestCount = 0;
      final adapter = _DownloadAdapter((_) {
        requestCount++;
        return _downloadResponse(<int>[], validator: 'unexpected');
      });
      final api = await _filesApi(adapter);
      final checkpoints = <DownloadCheckpoint>[];
      final progress = <(int, int)>[];

      final result = await api.downloadFile(
        logicalPath: '/payload.bin',
        destinationPath: '${directory.path}/payload.bin',
        stagingPath: part.path,
        resumeValidator: 'identity-stable',
        expectedTotalBytes: 4,
        preservePartialOnFailure: true,
        onCheckpoint: checkpoints.add,
        onProgress: (transferred, total) {
          progress.add((transferred, total));
        },
      );

      expect(requestCount, 0);
      expect(await result.file.readAsBytes(), <int>[1, 2, 3, 4]);
      expect(result.validator, 'identity-stable');
      expect(result.totalBytes, 4);
      expect(result.bytesWritten, 4);
      expect(checkpoints, hasLength(1));
      expect(checkpoints.single.validator, 'identity-stable');
      expect(checkpoints.single.durableOffset, 4);
      expect(checkpoints.single.totalBytes, 4);
      expect(progress, <(int, int)>[(4, 4)]);
      expect(part.existsSync(), isFalse);
    },
  );

  test('invalid resumed range keeps the partial file untouched', () async {
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-download-invalid-range-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final part = File('${directory.path}/task.part');
    await part.writeAsBytes(<int>[1, 2, 3], flush: true);
    final adapter = _DownloadAdapter(
      (_) => _downloadResponse(
        <int>[4, 5, 6],
        statusCode: HttpStatus.partialContent,
        validator: 'identity-stable',
        contentRange: 'bytes 2-4/6',
      ),
    );
    final api = await _filesApi(adapter);

    await expectLater(
      api.downloadFile(
        logicalPath: '/payload.bin',
        destinationPath: '${directory.path}/payload.bin',
        stagingPath: part.path,
        resumeValidator: 'identity-stable',
        expectedTotalBytes: 6,
        preservePartialOnFailure: true,
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'DOWNLOAD_RANGE_INVALID',
        ),
      ),
    );

    expect(await part.readAsBytes(), <int>[1, 2, 3]);
  });

  test('changed validator never appends to a partial file', () async {
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-download-validator-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final part = File('${directory.path}/task.part');
    await part.writeAsBytes(<int>[1, 2, 3], flush: true);
    final adapter = _DownloadAdapter(
      (_) => _downloadResponse(
        <int>[4, 5, 6],
        statusCode: HttpStatus.partialContent,
        validator: 'identity-changed',
        contentRange: 'bytes 3-5/6',
      ),
    );
    final api = await _filesApi(adapter);

    await expectLater(
      api.downloadFile(
        logicalPath: '/payload.bin',
        destinationPath: '${directory.path}/payload.bin',
        stagingPath: part.path,
        resumeValidator: 'identity-original',
        expectedTotalBytes: 6,
        preservePartialOnFailure: true,
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'DOWNLOAD_SOURCE_CHANGED',
        ),
      ),
    );

    expect(await part.readAsBytes(), <int>[1, 2, 3]);
  });

  test('truncated response preserves the durable partial file', () async {
    final directory = await Directory.systemTemp.createTemp(
      'mnemonas-download-truncated-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final part = File('${directory.path}/task.part');
    final adapter = _DownloadAdapter(
      (_) => ResponseBody(
        Stream<Uint8List>.value(Uint8List.fromList(<int>[1, 2])),
        HttpStatus.ok,
        headers: <String, List<String>>{
          Headers.contentLengthHeader: <String>['4'],
          downloadIdentityHeader: <String>['identity-1'],
        },
      ),
    );
    final api = await _filesApi(adapter);

    await expectLater(
      api.downloadFile(
        logicalPath: '/payload.bin',
        destinationPath: '${directory.path}/payload.bin',
        stagingPath: part.path,
        expectedTotalBytes: 4,
        preservePartialOnFailure: true,
      ),
      throwsA(
        isA<ApiException>().having(
          (error) => error.code,
          'code',
          'DOWNLOAD_TRUNCATED',
        ),
      ),
    );

    expect(await part.readAsBytes(), <int>[1, 2]);
  });
}

ResponseBody _downloadResponse(
  List<int> bytes, {
  int statusCode = HttpStatus.ok,
  required String validator,
  String? contentRange,
}) {
  return ResponseBody.fromBytes(
    bytes,
    statusCode,
    headers: <String, List<String>>{
      Headers.contentLengthHeader: <String>['${bytes.length}'],
      downloadIdentityHeader: <String>[validator],
      if (contentRange != null) 'content-range': <String>[contentRange],
    },
  );
}

Future<FilesApi> _filesApi(HttpClientAdapter adapter) async {
  final store = MemoryAuthSessionStore();
  final snapshot = await store.snapshot();
  final committed = await store.commitIfRevision(
    snapshot.revision,
    AuthSession(
      serverBaseUrl: 'https://nas.example.com',
      tokens: AuthTokenPair(
        accessToken: 'access-token',
        refreshToken: 'refresh-token',
        expiresAt: DateTime.now().toUtc().add(const Duration(hours: 1)),
      ),
    ),
  );
  expect(committed, isTrue);
  return FilesApi(
    ApiClient(
      endpoint: ServerEndpoint.parse('https://nas.example.com'),
      sessionStore: store,
      dio: Dio()..httpClientAdapter = adapter,
    ),
  );
}

final class _DownloadAdapter implements HttpClientAdapter {
  _DownloadAdapter(this._handler);

  final ResponseBody Function(RequestOptions options) _handler;

  @override
  void close({bool force = false}) {}

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    expect(options.method, 'GET');
    return _handler(options);
  }
}
