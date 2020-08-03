var guestbookApp = angular.module('guestbook', ['ui.bootstrap']);

/**
 * Constructor
 */
function guestbookController() {}

guestbookController.prototype.onguestbook = function() {
    this.scope_.messages.push(this.scope_.msg);
    this.scope_.msg = "";
    var value = this.scope_.messages.join();
    this.http_.get("guestbook.php?cmd=set&value=" + value)
            .success(angular.bind(this, function(data) {
                this.scope_.guestbookResponse = "Updated.";
            }));
};

guestbookApp.controller('guestbookCtrl', function ($scope, $http, $location) {
        $scope.controller = new guestbookController();
        $scope.controller.scope_ = $scope;
        $scope.controller.location_ = $location;
        $scope.controller.http_ = $http;

        $scope.controller.http_.get("guestbook.php?cmd=get")
            .success(function(data) {
                console.log(data);
                if (data.hasOwnProperty('error')) {
                    $scope.messages = [data.error];
                } else {
                    $scope.messages = data.data.toString().split(",");
                }
            });
});
