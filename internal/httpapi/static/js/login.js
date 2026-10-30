(function () {
  'use strict';
  var form = HP.$('#login-form'), msg = HP.$('#msg'), btn = HP.$('#go');
  form.addEventListener('submit', function (e) {
    e.preventDefault();
    msg.textContent = '';
    var email = HP.$('#email').value.trim(), password = HP.$('#password').value;
    if (!email || !password) { msg.textContent = 'Enter your email and password.'; return; }
    btn.disabled = true; btn.textContent = 'Signing in...';
    HP.post('/api/auth/login', { email: email, password: password }).then(function (r) {
      location.href = r.redirect;
    }, function (err) {
      msg.textContent = err.message;
      btn.disabled = false; btn.textContent = 'Sign in';
      HP.$('#password').select();
    });
  });
})();