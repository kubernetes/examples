<?php

error_reporting(E_ALL);
ini_set('display_errors', 1);
// echo extension_loaded("mongodb") ? "loaded\n" : "not loaded\n";

if (isset($_GET['cmd']) === true) {
  $host = 'mongo';
  if (getenv('GET_HOSTS_FROM') == 'env') {
    $host = getenv('MONGO_WRITE_HOSTS');
  }
  $mongo_host = "mongodb+srv://$host/guestbook?retryWrites=true&w=majority";
  header('Content-Type: application/json');
  // Create Guestbook Post
  if ($_GET['cmd'] == 'set') {
    $manager = new MongoDB\Driver\Manager("mongodb://$host");
    $bulk = new MongoDB\Driver\BulkWrite(['ordered' => true]);
    $bulk->insert(['message' => $_GET['value']]);
    try {
      $result = $manager->executeBulkWrite('guestbook.messages', $bulk);
    }
    catch (\MongoDB\Driver\Exception\Exception $e) {
      echo '{"error": "An error occured connecting to mongo server: ' . $host . '"}';
      exit;
    }
    print('{"message": "Updated"}');
  // Get Guestbook Post
  } else {
    $host = 'mongo';
    if (getenv('GET_HOSTS_FROM') == 'env') {
      $host = getenv('MONGO_READ_HOSTS');
    }
    $manager = new MongoDB\Driver\Manager("mongodb://$host");
    $query = new MongoDB\Driver\Query([]);
    try {
      $cursor = $manager->executeQuery('guestbook.messages', $query);
    }
    catch (\MongoDB\Driver\Exception\Exception $e) {
      echo '{"error": "An error occured connecting to mongo server: ' . $host . '"}';
      exit;
    }
    $data = array();
    foreach ($cursor as $document) {
      $data[] = $document->message;
    }
    print('{"data": ' . json_encode($data) . '}');
  }
} else {
  phpinfo();
} ?>
