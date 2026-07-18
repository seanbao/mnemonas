import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'transfer_task.dart';

final class TransferStoreSnapshot {
  TransferStoreSnapshot({
    required this.generation,
    required Iterable<TransferTask> tasks,
  }) : tasks = List<TransferTask>.unmodifiable(tasks);

  final int generation;
  final List<TransferTask> tasks;
}

final class FileTransferStore {
  FileTransferStore({required String directoryPath})
    : _directory = Directory(directoryPath) {
    if (!_isAbsoluteDirectoryPath(directoryPath)) {
      throw ArgumentError.value(
        directoryPath,
        'directoryPath',
        'Transfer store directory must be absolute',
      );
    }
  }

  static const int _ledgerSchemaVersion = 1;
  static const int _generationDigits = 16;
  static const int _maximumGeneration = 9999999999999999;
  static const int _maximumTasks = 10000;
  static const int _maximumLedgerBytes = 32 * 1024 * 1024;
  static const String _filePrefix = 'mnemonas-transfer-ledger-v1-g';
  static final RegExp _finalNamePattern = RegExp(
    '^$_filePrefix([0-9]{$_generationDigits})[.]json\$',
  );
  static final RegExp _partNamePattern = RegExp(
    '^$_filePrefix([0-9]{$_generationDigits})[.]json[.]part\$',
  );

  final Directory _directory;
  Future<void> _operationTail = Future<void>.value();

  String get directoryPath => _directory.path;

  Future<TransferStoreSnapshot> load() =>
      _serialized(() async => (await _scan()).snapshot);

  Future<TransferStoreSnapshot> replaceAll(Iterable<TransferTask> tasks) =>
      _serialized(() async {
        final scan = await _scan();
        final next = _normalizeTasks(tasks);
        _validateStableIdentities(scan.snapshot.tasks, next);
        return _persist(next, afterGeneration: scan.highestObservedGeneration);
      });

  Future<TransferStoreSnapshot> retainWhere(
    bool Function(TransferTask task) predicate,
  ) => _serialized(() async {
    final scan = await _scan();
    final next = scan.snapshot.tasks.where(predicate).toList(growable: false);
    if (next.length == scan.snapshot.tasks.length) {
      return scan.snapshot;
    }
    return _persist(next, afterGeneration: scan.highestObservedGeneration);
  });

  Future<TransferStoreSnapshot> upsert(TransferTask task) =>
      _serialized(() async {
        final scan = await _scan();
        final current = scan.snapshot.tasks;
        final existingIndex = current.indexWhere(
          (candidate) => candidate.id == task.id,
        );
        if (existingIndex >= 0) {
          _validateTaskUpdate(current[existingIndex], task);
        }
        final next = <TransferTask>[
          for (final candidate in current)
            if (candidate.id != task.id) candidate,
          task,
        ];
        return _persist(
          _normalizeTasks(next),
          afterGeneration: scan.highestObservedGeneration,
        );
      });

  Future<TransferStoreSnapshot> remove(String id) => _serialized(() async {
    _validateTaskId(id);
    final scan = await _scan();
    final next = scan.snapshot.tasks
        .where((task) => task.id != id)
        .toList(growable: false);
    if (next.length == scan.snapshot.tasks.length) {
      return scan.snapshot;
    }
    return _persist(next, afterGeneration: scan.highestObservedGeneration);
  });

  Future<TransferStoreSnapshot> _persist(
    List<TransferTask> tasks, {
    required int afterGeneration,
  }) async {
    if (afterGeneration >= _maximumGeneration) {
      throw const FileSystemException(
        'Transfer store generation space is exhausted',
      );
    }
    await _directory.create(recursive: true);
    final generation = afterGeneration + 1;
    final stem = _fileStem(generation);
    final part = File('${_directory.path}/$stem.json.part');
    final destination = File('${_directory.path}/$stem.json');
    final envelope = <String, Object?>{
      'schema_version': _ledgerSchemaVersion,
      'generation': generation,
      'tasks': tasks.map((task) => task.toJson()).toList(growable: false),
    };
    final encoded = utf8.encode('${jsonEncode(envelope)}\n');
    if (encoded.length > _maximumLedgerBytes) {
      throw const FormatException('Transfer ledger exceeds its size limit');
    }

    RandomAccessFile? output;
    try {
      output = await part.open(mode: FileMode.writeOnly);
      await output.truncate(0);
      await output.writeFrom(encoded);
      await output.flush();
      await output.close();
      output = null;
      await part.rename(destination.path);
    } on Object {
      await output?.close();
      rethrow;
    }

    await _cleanupOldGenerations(destination.path);
    return TransferStoreSnapshot(generation: generation, tasks: tasks);
  }

  Future<_StoreScan> _scan() async {
    if (!await _directory.exists()) {
      return _StoreScan(
        snapshot: TransferStoreSnapshot(generation: 0, tasks: const []),
        highestObservedGeneration: 0,
      );
    }

    final candidates = <_GenerationFile>[];
    var highestObserved = 0;
    try {
      await for (final entity in _directory.list(followLinks: false)) {
        final name = _basename(entity.path);
        final finalMatch = _finalNamePattern.firstMatch(name);
        final partMatch = _partNamePattern.firstMatch(name);
        final match = finalMatch ?? partMatch;
        if (match == null) {
          continue;
        }
        final generation = int.parse(match.group(1)!);
        if (finalMatch != null && generation > highestObserved) {
          highestObserved = generation;
        }
        if (finalMatch != null &&
            await FileSystemEntity.type(entity.path, followLinks: false) ==
                FileSystemEntityType.file) {
          candidates.add(
            _GenerationFile(generation: generation, file: File(entity.path)),
          );
        }
      }
    } on Object catch (error) {
      throw FileSystemException(
        'Unable to enumerate the transfer store',
        _directory.path,
        error is OSError ? error : null,
      );
    }

    candidates.sort(
      (left, right) => right.generation.compareTo(left.generation),
    );
    Object? readFailure;
    for (final candidate in candidates) {
      try {
        final snapshot = await _readGeneration(candidate);
        return _StoreScan(
          snapshot: snapshot,
          highestObservedGeneration: highestObserved,
        );
      } on FormatException {
        continue;
      } on FileSystemException catch (error) {
        readFailure ??= error;
      }
    }
    if (readFailure != null) {
      throw FileSystemException(
        'No readable transfer ledger generation is available',
        _directory.path,
      );
    }
    if (candidates.isNotEmpty) {
      throw const FormatException(
        'No valid transfer ledger generation is available',
      );
    }
    return _StoreScan(
      snapshot: TransferStoreSnapshot(generation: 0, tasks: const []),
      highestObservedGeneration: highestObserved,
    );
  }

  Future<TransferStoreSnapshot> _readGeneration(
    _GenerationFile candidate,
  ) async {
    final length = await candidate.file.length();
    if (length <= 0 || length > _maximumLedgerBytes) {
      throw const FormatException('Invalid transfer ledger size');
    }
    final decoded = jsonDecode(await candidate.file.readAsString());
    if (decoded is! Map || decoded.keys.any((key) => key is! String)) {
      throw const FormatException('Transfer ledger must be a JSON object');
    }
    final json = Map<String, dynamic>.from(decoded);
    const expectedKeys = <String>{'schema_version', 'generation', 'tasks'};
    if (json.length != expectedKeys.length ||
        expectedKeys.any((key) => !json.containsKey(key)) ||
        json['schema_version'] != _ledgerSchemaVersion ||
        json['generation'] != candidate.generation) {
      throw const FormatException('Invalid transfer ledger envelope');
    }
    final encodedTasks = json['tasks'];
    if (encodedTasks is! List || encodedTasks.length > _maximumTasks) {
      throw const FormatException('Invalid transfer ledger task list');
    }
    final tasks = _normalizeTasks(encodedTasks.map(TransferTask.fromJson));
    return TransferStoreSnapshot(
      generation: candidate.generation,
      tasks: tasks,
    );
  }

  Future<void> _cleanupOldGenerations(String currentPath) async {
    final candidates = <_GenerationFile>[];
    final parts = <File>[];
    try {
      await for (final entity in _directory.list(followLinks: false)) {
        final name = _basename(entity.path);
        final finalMatch = _finalNamePattern.firstMatch(name);
        if (finalMatch != null) {
          candidates.add(
            _GenerationFile(
              generation: int.parse(finalMatch.group(1)!),
              file: File(entity.path),
            ),
          );
        } else if (_partNamePattern.hasMatch(name)) {
          parts.add(File(entity.path));
        }
      }
    } on FileSystemException {
      return;
    }

    candidates.sort(
      (left, right) => right.generation.compareTo(left.generation),
    );
    final retainedPaths = <String>{currentPath};
    for (final candidate in candidates) {
      if (retainedPaths.contains(candidate.file.path)) {
        continue;
      }
      if (retainedPaths.length < 2) {
        try {
          await _readGeneration(candidate);
          retainedPaths.add(candidate.file.path);
          continue;
        } on Object {
          // Invalid older generations are not useful recovery points.
        }
      }
      await _deleteBestEffort(candidate.file);
    }
    for (final part in parts) {
      await _deleteBestEffort(part);
    }
  }

  Future<T> _serialized<T>(Future<T> Function() operation) {
    final result = Completer<T>();
    _operationTail = _operationTail.then((_) async {
      try {
        result.complete(await operation());
      } catch (error, stackTrace) {
        result.completeError(error, stackTrace);
      }
    });
    _operationTail = _operationTail.catchError((Object _) {});
    return result.future;
  }
}

final class _StoreScan {
  const _StoreScan({
    required this.snapshot,
    required this.highestObservedGeneration,
  });

  final TransferStoreSnapshot snapshot;
  final int highestObservedGeneration;
}

final class _GenerationFile {
  const _GenerationFile({required this.generation, required this.file});

  final int generation;
  final File file;
}

List<TransferTask> _normalizeTasks(Iterable<TransferTask> tasks) {
  final result = tasks.toList(growable: false);
  if (result.length > FileTransferStore._maximumTasks) {
    throw const FormatException('Transfer ledger contains too many tasks');
  }
  final ids = <String>{};
  for (final task in result) {
    if (!ids.add(task.id)) {
      throw const FormatException('Transfer task IDs must be unique');
    }
  }
  result.sort((left, right) {
    final created = left.createdAt.compareTo(right.createdAt);
    return created != 0 ? created : left.id.compareTo(right.id);
  });
  return List<TransferTask>.unmodifiable(result);
}

void _validateStableIdentities(
  List<TransferTask> current,
  List<TransferTask> next,
) {
  final currentById = <String, TransferTask>{
    for (final task in current) task.id: task,
  };
  for (final task in next) {
    final previous = currentById[task.id];
    if (previous != null) {
      _validateTaskUpdate(previous, task);
    }
  }
}

void _validateTaskUpdate(TransferTask previous, TransferTask next) {
  if (!previous.hasSameIdentityAs(next)) {
    throw const FormatException(
      'A transfer task cannot change its stable identity',
    );
  }
  if (previous.uploadSessionCreateAttempted &&
      !next.uploadSessionCreateAttempted) {
    throw const FormatException(
      'An upload session create attempt cannot be reverted',
    );
  }
}

void _validateTaskId(String id) {
  if (id.isEmpty ||
      id.trim() != id ||
      id.runes.length > 128 ||
      !RegExp(r'^[A-Za-z0-9][A-Za-z0-9._:-]*$').hasMatch(id)) {
    throw const FormatException('Invalid transfer task id');
  }
}

String _fileStem(int generation) {
  final encoded = generation.toString().padLeft(
    FileTransferStore._generationDigits,
    '0',
  );
  return '${FileTransferStore._filePrefix}$encoded';
}

String _basename(String path) {
  final normalized = path.replaceAll(r'\', '/');
  final separator = normalized.lastIndexOf('/');
  return separator < 0 ? normalized : normalized.substring(separator + 1);
}

bool _isAbsoluteDirectoryPath(String value) {
  if (value.isEmpty || value.runes.any((rune) => rune < 0x20 || rune == 0x7f)) {
    return false;
  }
  return value.startsWith('/') ||
      RegExp(r'^[A-Za-z]:[\\/]').hasMatch(value) ||
      value.startsWith(r'\\') ||
      value.startsWith('//');
}

Future<void> _deleteBestEffort(File file) async {
  try {
    if (await file.exists()) {
      await file.delete();
    }
  } on FileSystemException {
    // A retained generation is preferable to failing an already durable write.
  }
}
