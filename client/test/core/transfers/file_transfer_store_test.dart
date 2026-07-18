import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/transfers/file_transfer_store.dart';
import 'package:mnemonas_client/core/transfers/transfer_task.dart';

void main() {
  late Directory directory;

  setUp(() async {
    directory = await Directory.systemTemp.createTemp(
      'mnemonas-transfer-store-',
    );
  });

  tearDown(() async {
    if (await directory.exists()) {
      await directory.delete(recursive: true);
    }
  });

  test('a new store is empty and rejects a relative storage path', () async {
    final snapshot = await _store(directory).load();

    expect(snapshot.generation, 0);
    expect(snapshot.tasks, isEmpty);
    expect(
      () => FileTransferStore(directoryPath: 'relative/transfers'),
      throwsArgumentError,
    );
  });

  test('tasks and durable offsets survive a store restart', () async {
    final firstStore = _store(directory);
    final first = _task('task-1');
    final second = _task(
      'task-2',
      phase: TransferPhase.paused,
      durableOffset: 48,
      errorCode: 'NETWORK_PAUSED',
      errorMessage: 'Network unavailable',
    );

    await firstStore.upsert(first);
    final written = await firstStore.upsert(second);
    final restored = await _store(directory).load();

    expect(written.generation, 2);
    expect(restored.generation, written.generation);
    expect(restored.tasks, <TransferTask>[first, second]);
    expect(await _generationFiles(directory), hasLength(2));
  });

  test(
    'startup ignores a residual part and falls back from corruption',
    () async {
      final store = _store(directory);
      final first = _task('task-1');
      final second = _task('task-2');
      await store.replaceAll(<TransferTask>[first]);
      await store.replaceAll(<TransferTask>[first, second]);
      final generations = await _generationFiles(directory);
      expect(generations, hasLength(2));

      await generations.last.writeAsString('{"corrupt":');
      final residual = File(
        '${directory.path}/'
        'mnemonas-transfer-ledger-v1-g9999999999999999.json.part',
      );
      await residual.writeAsString(
        jsonEncode(<String, Object?>{
          'schema_version': 1,
          'generation': 9999999999999999,
          'tasks': <Object?>[],
        }),
      );

      final restored = await _store(directory).load();

      expect(restored.generation, 1);
      expect(restored.tasks, <TransferTask>[first]);

      final saved = await _store(directory).upsert(_task('task-3'));
      expect(saved.generation, 3);
      expect(saved.tasks.map((task) => task.id), <String>['task-1', 'task-3']);
      expect(await _generationFiles(directory), hasLength(2));
      expect(await _partFiles(directory), isEmpty);
    },
  );

  test('concurrent upserts serialize without losing tasks', () async {
    final store = _store(directory);
    final tasks = <TransferTask>[
      for (var index = 0; index < 8; index++) _task('task-$index'),
    ];

    await Future.wait(tasks.map(store.upsert));
    final restored = await _store(directory).load();

    expect(restored.generation, tasks.length);
    expect(restored.tasks.map((task) => task.id), <String>[
      for (var index = 0; index < 8; index++) 'task-$index',
    ]);
    expect(await _generationFiles(directory), hasLength(2));
    expect(await _partFiles(directory), isEmpty);
  });

  test('concurrent retain and upsert serialize without losing tasks', () async {
    final store = _store(directory);
    final removable = _task(
      'task-1',
      phase: TransferPhase.completed,
      durableOffset: 100,
    );
    final retained = _task('task-2');
    final concurrent = _task('task-3');
    await store.replaceAll(<TransferTask>[removable, retained]);

    final prune = store.retainWhere((task) => task.id != removable.id);
    final insert = store.upsert(concurrent);
    await Future.wait(<Future<TransferStoreSnapshot>>[prune, insert]);
    final restored = await _store(directory).load();

    expect(restored.tasks.map((task) => task.id), <String>[
      retained.id,
      concurrent.id,
    ]);
  });

  test('store rejects duplicate IDs and stable scope changes', () async {
    final store = _store(directory);
    final original = _task('task-1');
    await store.upsert(original);

    await expectLater(
      store.replaceAll(<TransferTask>[original, original]),
      throwsFormatException,
    );
    await expectLater(
      store.upsert(
        TransferTask(
          id: original.id,
          direction: original.direction,
          phase: original.phase,
          endpointBaseUrl: 'https://other.example.com',
          userId: original.userId,
          remotePath: original.remotePath,
          displayName: original.displayName,
          stagingPath: original.stagingPath,
          destinationPath: original.destinationPath,
          destinationUri: original.destinationUri,
          durableOffset: original.durableOffset,
          totalBytes: original.totalBytes,
          createdAt: original.createdAt,
          updatedAt: original.updatedAt,
        ),
      ),
      throwsFormatException,
    );

    final restored = await _store(directory).load();
    expect(restored.tasks, <TransferTask>[original]);
  });

  test('store never reverts an upload create-attempt marker', () async {
    final store = _store(directory);
    final attempted = _uploadTask('task-9', uploadSessionCreateAttempted: true);
    await store.upsert(attempted);

    await expectLater(
      store.upsert(attempted.copyWith(uploadSessionCreateAttempted: false)),
      throwsFormatException,
    );

    final restored = await _store(directory).load();
    expect(restored.tasks.single.uploadSessionCreateAttempted, isTrue);
  });

  test('strict ledger decoding rejects hidden token data', () async {
    final file = File(
      '${directory.path}/'
      'mnemonas-transfer-ledger-v1-g0000000000000001.json',
    );
    final taskJson = _task('task-1').toJson();
    await file.writeAsString(
      jsonEncode(<String, Object?>{
        'schema_version': 1,
        'generation': 1,
        'tasks': <Object?>[
          <String, Object?>{...taskJson, 'refresh_token': 'must-not-be-stored'},
        ],
      }),
    );

    await expectLater(_store(directory).load(), throwsFormatException);
  });
}

FileTransferStore _store(Directory directory) =>
    FileTransferStore(directoryPath: directory.path);

TransferTask _task(
  String id, {
  TransferPhase phase = TransferPhase.queued,
  int durableOffset = 0,
  String? errorCode,
  String? errorMessage,
}) {
  final index = int.parse(id.split('-').last);
  return TransferTask(
    id: id,
    direction: TransferDirection.download,
    phase: phase,
    endpointBaseUrl: 'https://nas.example.com',
    userId: 'user-1',
    remotePath: '/documents/report-$index.pdf',
    displayName: 'report-$index.pdf',
    stagingPath: '${directorySafeRoot()}/task-$index.part',
    destinationPath: '${directorySafeRoot()}/report-$index.pdf',
    durableOffset: durableOffset,
    totalBytes: 100,
    createdAt: DateTime.utc(2026, 7, 19, 12, index),
    updatedAt: DateTime.utc(2026, 7, 19, 12, index),
    errorCode: errorCode,
    errorMessage: errorMessage,
  );
}

TransferTask _uploadTask(String id, {bool? uploadSessionCreateAttempted}) {
  final index = int.parse(id.split('-').last);
  return TransferTask(
    id: id,
    direction: TransferDirection.upload,
    phase: TransferPhase.running,
    endpointBaseUrl: 'https://nas.example.com',
    userId: 'user-1',
    remotePath: '/documents/report-$index.pdf',
    displayName: 'report-$index.pdf',
    stagingPath: '${directorySafeRoot()}/task-$index.upload',
    payloadSha256:
        '0123456789abcdef0123456789abcdef'
        '0123456789abcdef0123456789abcdef',
    uploadSessionCreateAttempted: uploadSessionCreateAttempted,
    durableOffset: 0,
    totalBytes: 100,
    createdAt: DateTime.utc(2026, 7, 19, 12, index),
    updatedAt: DateTime.utc(2026, 7, 19, 12, index),
  );
}

String directorySafeRoot() =>
    Platform.isWindows ? r'C:\mnemonas\transfers' : '/private/transfers';

Future<List<File>> _generationFiles(Directory directory) async {
  final files = await directory
      .list()
      .where((entity) => entity is File && entity.path.endsWith('.json'))
      .cast<File>()
      .toList();
  files.sort((left, right) => left.path.compareTo(right.path));
  return files;
}

Future<List<File>> _partFiles(Directory directory) async {
  return directory
      .list()
      .where((entity) => entity is File && entity.path.endsWith('.part'))
      .cast<File>()
      .toList();
}
