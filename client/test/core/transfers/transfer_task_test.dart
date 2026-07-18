import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/transfers/transfer_task.dart';

void main() {
  test('strict serialization round-trips every transfer field', () {
    final task = _task(
      phase: TransferPhase.running,
      durableOffset: 32,
      validator: 'W/"object-1"',
      errorCode: 'NETWORK_PAUSED',
      errorMessage: 'Network unavailable',
    );

    expect(TransferTask.fromJson(task.toJson()), task);
    expect(task.toJson().keys.toSet(), <String>{
      'schema_version',
      'id',
      'direction',
      'phase',
      'endpoint_base_url',
      'user_id',
      'remote_path',
      'display_name',
      'staging_path',
      'destination_path',
      'destination_uri',
      'validator',
      'payload_sha256',
      'upload_session_id',
      'upload_session_create_attempted',
      'upload_session_expires_at',
      'durable_offset',
      'total_bytes',
      'created_at',
      'updated_at',
      'error_code',
      'error_message',
    });
    expect(
      TransferTask.fromJson(<String, Object?>{
        ...task.toJson(),
        'created_at': '2026-07-19T12:00:00.123456Z',
        'updated_at': '2026-07-19T12:00:00.123456Z',
      }).createdAt.microsecondsSinceEpoch,
      DateTime.utc(2026, 7, 19, 12, 0, 0, 123, 456).microsecondsSinceEpoch,
    );
  });

  test('upload session fields round-trip with normalized UTC expiry', () {
    final task = _uploadTask(
      phase: TransferPhase.running,
      durableOffset: 32,
      uploadSessionId: 'session.upload-1',
      uploadSessionExpiresAt: DateTime.parse('2026-07-19T21:00:00+08:00'),
    );

    final restored = TransferTask.fromJson(task.toJson());

    expect(restored, task);
    expect(restored.uploadSessionCreateAttempted, isTrue);
    expect(restored.uploadSessionExpiresAt?.isUtc, isTrue);
    expect(
      restored.toJson()['upload_session_expires_at'],
      '2026-07-19T13:00:00.000Z',
    );
  });

  test('schema 2 upload tasks migrate create attempts conservatively', () {
    Map<String, Object?> schema2(TransferTask task) =>
        Map<String, Object?>.from(task.toJson())
          ..['schema_version'] = 2
          ..remove('upload_session_create_attempted');

    final queued = TransferTask.fromJson(schema2(_uploadTask()));
    final running = TransferTask.fromJson(
      schema2(_uploadTask(phase: TransferPhase.running)),
    );
    final withSession = TransferTask.fromJson(
      schema2(
        _uploadTask(
          phase: TransferPhase.paused,
          uploadSessionId: 'session-1',
          uploadSessionExpiresAt: DateTime.utc(2026, 7, 20, 12),
        ),
      ),
    );
    final download = TransferTask.fromJson(schema2(_task()));

    expect(queued.schemaVersion, TransferTask.currentSchemaVersion);
    expect(queued.uploadSessionCreateAttempted, isFalse);
    expect(running.uploadSessionCreateAttempted, isTrue);
    expect(withSession.uploadSessionCreateAttempted, isTrue);
    expect(download.uploadSessionCreateAttempted, isFalse);
  });

  test(
    'deserialization rejects schema 1, missing, unknown, and token fields',
    () {
      final json = _task().toJson();

      expect(
        () => TransferTask.fromJson(<String, Object?>{
          ...json,
          'schema_version': 1,
        }),
        throwsFormatException,
      );
      expect(
        () => TransferTask.fromJson(<String, Object?>{
          ...json,
          'access_token': 'must-not-be-stored',
        }),
        throwsFormatException,
      );
      expect(
        () => TransferTask.fromJson(
          Map<String, Object?>.from(json)..remove('payload_sha256'),
        ),
        throwsFormatException,
      );
      expect(
        () => TransferTask.fromJson(
          Map<String, Object?>.from(json)..remove('user_id'),
        ),
        throwsFormatException,
      );
      expect(
        () => TransferTask.fromJson(
          Map<String, Object?>.from(json)
            ..remove('upload_session_create_attempted'),
        ),
        throwsFormatException,
      );
      expect(
        () => TransferTask.fromJson(<String, Object?>{
          ...json,
          'direction': 'sideways',
        }),
        throwsFormatException,
      );
    },
  );

  test('byte, terminal, error, and destination invariants are enforced', () {
    expect(() => _task(durableOffset: 101), throwsFormatException);
    expect(
      () => _task(
        phase: TransferPhase.completed,
        durableOffset: 100,
        destinationPath: null,
      ),
      throwsFormatException,
    );
    expect(
      () => _task(phase: TransferPhase.failed, durableOffset: 10),
      throwsFormatException,
    );
    expect(
      () => _uploadTask(destinationPath: '/downloads/report.pdf'),
      throwsFormatException,
    );
    expect(
      () => _task(
        destinationPath: '/downloads/report.pdf',
        destinationUri: 'content://downloads/report',
      ),
      throwsFormatException,
    );
    expect(
      () => _task(
        phase: TransferPhase.awaitingDestination,
        durableOffset: 99,
        destinationPath: null,
        destinationUri: 'content://downloads/report',
      ),
      throwsFormatException,
    );
    expect(
      _task(
        phase: TransferPhase.awaitingDestination,
        durableOffset: 100,
        destinationPath: null,
        destinationUri: null,
      ).phase,
      TransferPhase.awaitingDestination,
    );
  });

  test('upload and download fields are direction-specific', () {
    expect(() => _uploadTask(payloadSha256: null), throwsFormatException);
    expect(
      () => _uploadTask(payloadSha256: _repeat('A', 64)),
      throwsFormatException,
    );
    expect(
      () => _uploadTask(payloadSha256: _repeat('g', 64)),
      throwsFormatException,
    );
    expect(
      () => _uploadTask(payloadSha256: _repeat('a', 63)),
      throwsFormatException,
    );
    expect(() => _uploadTask(validator: 'W/"object-1"'), throwsFormatException);
    expect(() => _task(payloadSha256: _repeat('a', 64)), throwsFormatException);
    expect(() => _task(uploadSessionId: 'session-1'), throwsFormatException);
    expect(
      () => _task(uploadSessionExpiresAt: DateTime.utc(2026, 7, 19, 13)),
      throwsFormatException,
    );
  });

  test('upload session identifier and expiry are strict', () {
    expect(
      () => _uploadTask(uploadSessionId: 'unsafe/session'),
      throwsFormatException,
    );
    expect(
      () => _uploadTask(uploadSessionId: 'session-1'),
      throwsFormatException,
    );
    expect(
      () => _uploadTask(uploadSessionExpiresAt: DateTime.utc(2026, 7, 20, 12)),
      throwsFormatException,
    );
    expect(
      () => _uploadTask(
        uploadSessionExpiresAt: DateTime.utc(2026, 7, 19, 11, 59, 59),
      ),
      throwsFormatException,
    );
    expect(
      () => _uploadTask(
        uploadSessionId: 'session-1',
        uploadSessionCreateAttempted: false,
        uploadSessionExpiresAt: DateTime.utc(2026, 7, 20, 12),
      ),
      throwsFormatException,
    );
    expect(
      _uploadTask(
        uploadSessionId: 'session_1:v2',
        uploadSessionExpiresAt: DateTime.utc(2026, 7, 19, 12),
      ).uploadSessionExpiresAt,
      DateTime.utc(2026, 7, 19, 12),
    );
    expect(
      () => TransferTask.fromJson(<String, Object?>{
        ..._uploadTask().toJson(),
        'upload_session_expires_at': '2026-07-19T20:00:00+08:00',
      }),
      throwsFormatException,
    );
  });

  test('scope, paths, and timestamps are strict', () {
    expect(
      () => _task(endpointBaseUrl: 'https://NAS.example.com/'),
      throwsFormatException,
    );
    expect(
      () => _task(remotePath: '/documents/../report.pdf'),
      throwsFormatException,
    );
    expect(
      () => _task(stagingPath: 'relative/report.part'),
      throwsFormatException,
    );
    expect(
      () => TransferTask.fromJson(<String, Object?>{
        ..._task().toJson(),
        'created_at': '2026-02-31T00:00:00Z',
      }),
      throwsFormatException,
    );
  });

  test(
    'stable identity includes payload but excludes mutable session fields',
    () {
      final original = _uploadTask();
      final withSession = original.copyWith(
        phase: TransferPhase.paused,
        uploadSessionId: 'session-1',
        uploadSessionCreateAttempted: true,
        uploadSessionExpiresAt: DateTime.utc(2026, 7, 20, 12),
        durableOffset: 20,
        updatedAt: DateTime.utc(2026, 7, 19, 12, 1),
      );

      expect(original.hasSameIdentityAs(withSession), isTrue);
      expect(
        withSession.copyWith(
          uploadSessionId: null,
          uploadSessionExpiresAt: null,
        ),
        original.copyWith(
          phase: TransferPhase.paused,
          uploadSessionCreateAttempted: true,
          durableOffset: 20,
          updatedAt: DateTime.utc(2026, 7, 19, 12, 1),
        ),
      );
      expect(
        original.hasSameIdentityAs(
          _uploadTask(payloadSha256: _repeat('b', 64)),
        ),
        isFalse,
      );
    },
  );
}

TransferTask _task({
  TransferPhase phase = TransferPhase.queued,
  String endpointBaseUrl = 'https://nas.example.com',
  String remotePath = '/documents/report.pdf',
  String stagingPath = '/private/transfers/task-1.part',
  String? destinationPath = '/downloads/report.pdf',
  String? destinationUri,
  String? validator,
  String? payloadSha256,
  String? uploadSessionId,
  bool? uploadSessionCreateAttempted,
  DateTime? uploadSessionExpiresAt,
  int durableOffset = 0,
  String? errorCode,
  String? errorMessage,
}) {
  return TransferTask(
    id: 'task-1',
    direction: TransferDirection.download,
    phase: phase,
    endpointBaseUrl: endpointBaseUrl,
    userId: 'user-1',
    remotePath: remotePath,
    displayName: 'report.pdf',
    stagingPath: stagingPath,
    destinationPath: destinationPath,
    destinationUri: destinationUri,
    validator: validator,
    payloadSha256: payloadSha256,
    uploadSessionId: uploadSessionId,
    uploadSessionCreateAttempted: uploadSessionCreateAttempted,
    uploadSessionExpiresAt: uploadSessionExpiresAt,
    durableOffset: durableOffset,
    totalBytes: 100,
    createdAt: DateTime.utc(2026, 7, 19, 12),
    updatedAt: DateTime.utc(2026, 7, 19, 12),
    errorCode: errorCode,
    errorMessage: errorMessage,
  );
}

TransferTask _uploadTask({
  TransferPhase phase = TransferPhase.queued,
  String? payloadSha256 = _payloadSha256,
  String? uploadSessionId,
  bool? uploadSessionCreateAttempted,
  DateTime? uploadSessionExpiresAt,
  String? destinationPath,
  String? destinationUri,
  String? validator,
  int durableOffset = 0,
}) {
  return TransferTask(
    id: 'task-1',
    direction: TransferDirection.upload,
    phase: phase,
    endpointBaseUrl: 'https://nas.example.com',
    userId: 'user-1',
    remotePath: '/documents/report.pdf',
    displayName: 'report.pdf',
    stagingPath: '/private/transfers/task-1.payload',
    destinationPath: destinationPath,
    destinationUri: destinationUri,
    validator: validator,
    payloadSha256: payloadSha256,
    uploadSessionId: uploadSessionId,
    uploadSessionCreateAttempted: uploadSessionCreateAttempted,
    uploadSessionExpiresAt: uploadSessionExpiresAt,
    durableOffset: durableOffset,
    totalBytes: 100,
    createdAt: DateTime.utc(2026, 7, 19, 12),
    updatedAt: DateTime.utc(2026, 7, 19, 12),
  );
}

const String _payloadSha256 =
    '0123456789abcdef0123456789abcdef'
    '0123456789abcdef0123456789abcdef';

String _repeat(String value, int count) => List.filled(count, value).join();
