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

  test('deserialization rejects missing, unknown, and token fields', () {
    final json = _task().toJson();

    expect(
      () => TransferTask.fromJson(<String, Object?>{
        ...json,
        'access_token': 'must-not-be-stored',
      }),
      throwsFormatException,
    );
    expect(
      () => TransferTask.fromJson(
        Map<String, Object?>.from(json)..remove('user_id'),
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
  });

  test('byte, terminal, error, and direction invariants are enforced', () {
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
      () => _task(
        direction: TransferDirection.upload,
        destinationPath: '/downloads/report.pdf',
      ),
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

  test('scope, paths, timestamps, and stable identity are strict', () {
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

    final original = _task();
    expect(
      original.hasSameIdentityAs(
        original.copyWith(
          phase: TransferPhase.paused,
          durableOffset: 20,
          updatedAt: DateTime.utc(2026, 7, 19, 12, 1),
        ),
      ),
      isTrue,
    );
  });
}

TransferTask _task({
  TransferDirection direction = TransferDirection.download,
  TransferPhase phase = TransferPhase.queued,
  String endpointBaseUrl = 'https://nas.example.com',
  String remotePath = '/documents/report.pdf',
  String stagingPath = '/private/transfers/task-1.part',
  String? destinationPath = '/downloads/report.pdf',
  String? destinationUri,
  String? validator,
  int durableOffset = 0,
  String? errorCode,
  String? errorMessage,
}) {
  return TransferTask(
    id: 'task-1',
    direction: direction,
    phase: phase,
    endpointBaseUrl: endpointBaseUrl,
    userId: 'user-1',
    remotePath: remotePath,
    displayName: 'report.pdf',
    stagingPath: stagingPath,
    destinationPath: destinationPath,
    destinationUri: destinationUri,
    validator: validator,
    durableOffset: durableOffset,
    totalBytes: 100,
    createdAt: DateTime.utc(2026, 7, 19, 12),
    updatedAt: DateTime.utc(2026, 7, 19, 12),
    errorCode: errorCode,
    errorMessage: errorMessage,
  );
}
